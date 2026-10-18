#!/usr/bin/env python3
"""PERF-01: API / reconciliation scale, not Bitrix application load."""
import argparse
import copy
import json
import math
import pathlib
import subprocess
import sys
import time
import uuid
from datetime import datetime, timezone
from concurrent.futures import ThreadPoolExecutor

RUN_LABEL = "perf.bitrix.m8.team/run"
SIZES = (10, 100, 1000)


def percentile(values, quantile):
    if not values:
        return None
    ordered = sorted(values)
    return round(ordered[max(0, math.ceil(quantile * len(ordered)) - 1)], 3)


def observed(resource):
    meta = resource.get("metadata", {})
    status = resource.get("status") or {}
    generation = meta.get("generation")
    if not isinstance(generation, int) or status.get("observedGeneration") != generation:
        return False
    return any(
        c.get("type") == "Ready" and c.get("observedGeneration") == generation
        and c.get("status") == "False" and c.get("reason") == "SecretMissing"
        for c in status.get("conditions", [])
    )


def kubectl(context, args, payload=None, timeout=300):
    proc = subprocess.run(
        ["kubectl", "--context", context, *args],
        input=json.dumps(payload) if payload is not None else None,
        capture_output=True, text=True, timeout=timeout, check=False,
    )
    if proc.returncode != 0:
        # Never include stderr or submitted CR fields in CI errors.
        operation = next((x for x in args if x in ("get", "create", "patch", "delete")), "command")
        raise RuntimeError("kubectl " + operation + " failed (exit " + str(proc.returncode) + ")")
    return proc.stdout


def collect(context, namespace, size, start, seconds, poll):
    deadline = time.monotonic() + seconds
    durations = {}
    while True:
        snapshot = json.loads(kubectl(context, [
            "-n", namespace, "get", "bitrixclusters", "-o", "json",
        ], timeout=60))["items"]
        now = time.monotonic()
        for resource in snapshot:
            if observed(resource):
                durations.setdefault(resource["metadata"]["name"], now - start)
        if len(durations) == size or now >= deadline:
            break
        time.sleep(poll)
    sample = list(durations.values())
    return {
        "observed": len(durations), "pending": size - len(durations),
        "complete": len(durations) == size,
        "elapsed_seconds": round(time.monotonic() - start, 3),
        "p50_observed_upper_bound_seconds": percentile(sample, .50),
        "p95_observed_upper_bound_seconds": percentile(sample, .95),
        "p99_observed_upper_bound_seconds": percentile(sample, .99),
        "poll_seconds": poll,
    }


def manifests(sample, namespace, run_id, count):
    values = []
    for i in range(count):
        resource = copy.deepcopy(sample)
        resource["metadata"] = {
            "name": "perf-%04d" % i, "namespace": namespace,
            "labels": {RUN_LABEL: run_id},
        }
        values.append(resource)
    return values


def patch_objects(context, namespace, records, payload, workers):
    """Patch only the CRs created by this test, at bounded concurrency.

    kubectl patch requires named resources or -f; unlike kubectl get/delete,
    it does not accept a bare -l selector.
    """
    patch = json.dumps(payload)

    def patch_one(record):
        name = record["metadata"]["name"]
        kubectl(context, [
            "-n", namespace, "patch", "bitrixcluster", name,
            "--type=merge", "-p", patch, "-o", "name",
        ], timeout=90)

    with ThreadPoolExecutor(max_workers=workers) as executor:
        for _ in executor.map(patch_one, records):
            pass


def exercise(context, sample, run_id, count, args):
    ns = "bitrix-perf-" + run_id + "-" + str(count)
    kubectl(context, ["create", "namespace", ns], timeout=45)
    result = {"size": count, "create": None, "spec_update": None}
    phase = "create"
    try:
        try:
            resources = manifests(sample, ns, run_id, count)
            started = time.monotonic()
            for offset in range(0, count, args.batch_size):
                payload = {
                    "apiVersion": "v1", "kind": "List",
                    "items": resources[offset:offset + args.batch_size],
                }
                kubectl(context, ["create", "-f", "-", "-o", "name"], payload, timeout=240)
            result["create"] = collect(context, ns, count, started, args.timeout, args.poll)
            if not result["create"]["complete"]:
                return result
            phase = "spec_update"
            started = time.monotonic()
            patch = {"spec": {"release": {"image": {"tag": "perf-" + run_id}}}}
            patch_objects(context, ns, resources, patch, args.patch_workers)
            result["spec_update"] = collect(context, ns, count, started, args.timeout, args.poll)
        except (RuntimeError, subprocess.TimeoutExpired) as exc:
            # Keep finished phase measurements in the uploaded artifact.
            # Do not serialize untrusted stderr or manifest contents.
            result["failure"] = {"phase": phase, "type": type(exc).__name__, "message": str(exc) if isinstance(exc, RuntimeError) else "kubectl timeout"}
        return result
    finally:
        if args.keep:
            print("Retained temporary test namespace:", ns, flush=True)
        else:
            kubectl(context, ["delete", "namespace", ns, "--wait=true", "--timeout=300s"], timeout=330)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--sizes", type=int, nargs="+", default=list(SIZES))
    parser.add_argument("--sample", default="config/samples/bitrix_v1alpha1_bitrixcluster.yaml")
    parser.add_argument("--output", default="perf-results.json")
    parser.add_argument("--timeout", type=float, default=300)
    parser.add_argument("--poll", type=float, default=1)
    parser.add_argument("--batch-size", type=int, default=100)
    parser.add_argument("--patch-workers", type=int, default=8)
    parser.add_argument("--keep", action="store_true")
    args = parser.parse_args(argv)
    if not args.context.startswith("kind-"):
        parser.error("Refusing a non-kind context; only disposable kind clusters are supported")
    if len(set(args.sizes)) != len(args.sizes) or not args.sizes or any(n not in SIZES for n in args.sizes):
        parser.error("sizes must be a unique subset of 10, 100, 1000")
    if args.timeout <= 0 or args.poll <= 0 or not 1 <= args.batch_size <= 250 or not 1 <= args.patch_workers <= 16:
        parser.error("invalid timeout, poll, batch-size or patch-workers (max 16)")
    if not pathlib.Path(args.sample).is_file():
        parser.error("sample file does not exist")
    kubectl(args.context, ["get", "crd", "bitrixclusters.bitrix.m8.team"], timeout=30)
    sample = json.loads(kubectl(args.context, [
        "create", "--dry-run=client", "-f", args.sample, "-o", "json",
    ], timeout=45))
    if sample.get("kind") != "BitrixCluster" or sample.get("apiVersion") != "bitrix.m8.team/v1alpha1":
        parser.error("sample is not a BitrixCluster v1alpha1")
    if ((sample.get("spec", {}).get("networking") or {}).get("gateway") or {}).get("enabled"):
        parser.error("baseline does not support Gateway publication")
    run_id = uuid.uuid4().hex[:8]
    report = {
        "version": 1, "scenario": "missing-secrets-control-plane",
        "scope": "CR create and spec update; no workload readiness",
        "run_id": run_id, "context": args.context,
        "started_at": datetime.now(timezone.utc).isoformat(), "sizes": [],
    }
    failed = False
    try:
        for count in args.sizes:
            result = exercise(args.context, sample, run_id, count, args)
            report["sizes"].append(result)
            print(json.dumps(result, sort_keys=True), flush=True)
            if result.get("failure") or result["create"] is None or not result["create"]["complete"] or (
                result["spec_update"] is not None and not result["spec_update"]["complete"]
            ):
                failed = True
                break
    finally:
        pathlib.Path(args.output).parent.mkdir(parents=True, exist_ok=True)
        pathlib.Path(args.output).write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    return int(failed)


if __name__ == "__main__":
    sys.exit(main())
