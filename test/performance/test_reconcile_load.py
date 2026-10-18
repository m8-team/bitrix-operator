"""Hermetic performance harness tests; no kubeconfig or kind required."""
import copy
import argparse
import importlib.util
import pathlib
import json
import subprocess
import unittest
from unittest.mock import patch

file = pathlib.Path(__file__).parent / "reconcile_load.py"
spec = importlib.util.spec_from_file_location("reconcile_load", file)
load = importlib.util.module_from_spec(spec)
spec.loader.exec_module(load)


class TestHarness(unittest.TestCase):
    def test_nearest_rank_quantiles(self):
        self.assertEqual(load.percentile([9, 1, 4, 2], .5), 2)
        self.assertEqual(load.percentile([9, 1, 4, 2], .95), 9)
        self.assertIsNone(load.percentile([], .95))

    def test_observed_requires_exact_generation_and_reason(self):
        resource = {"metadata": {"generation": 2}, "status": {
            "observedGeneration": 2, "conditions": [
                {"type": "Ready", "status": "False",
                 "reason": "SecretMissing", "observedGeneration": 2},
            ],
        }}
        self.assertTrue(load.observed(resource))
        for field, value in [("status", "True"), ("reason", "WebReady"),
                             ("observedGeneration", 1)]:
            changed = copy.deepcopy(resource)
            changed["status"]["conditions"][0][field] = value
            self.assertFalse(load.observed(changed))
        resource["status"]["observedGeneration"] = 1
        self.assertFalse(load.observed(resource))

    def test_manifests_are_isolated(self):
        template = {"metadata": {"name": "example", "namespace": "default"}}
        items = load.manifests(template, "test", "run", 10)
        self.assertEqual(len({x["metadata"]["name"] for x in items}), 10)
        self.assertTrue(all(x["metadata"]["namespace"] == "test" for x in items))
        self.assertEqual(template["metadata"]["namespace"], "default")

    def test_guard_rejects_non_kind(self):
        with self.assertRaises(SystemExit):
            load.main(["--context", "production-cluster"])

    def test_patch_is_scoped_to_each_created_object(self):
        sample = {"metadata": {"name": "sample"}}
        records = load.manifests(sample, "bitrix-perf-test", "12345678", 10)
        payload = {"spec": {"release": {"image": {"tag": "perf-12345678"}}}}
        with patch.object(load, "kubectl", return_value="") as command:
            load.patch_objects("kind-ci", "bitrix-perf-test", records, payload, 4)
        self.assertEqual(command.call_count, 10)
        names = []
        for invocation in command.call_args_list:
            context, argv = invocation.args[:2]
            self.assertEqual(context, "kind-ci")
            self.assertEqual(argv[:4], ["-n", "bitrix-perf-test", "patch", "bitrixcluster"])
            self.assertNotIn("-l", argv)
            self.assertEqual(argv[5:7], ["--type=merge", "-p"])
            self.assertEqual(json.loads(argv[7]), payload)
            names.append(argv[4])
        self.assertEqual(sorted(names), sorted(x["metadata"]["name"] for x in records))

    def test_update_error_keeps_create_measurement(self):
        args = argparse.Namespace(batch_size=100, patch_workers=2, timeout=1, poll=0.1, keep=True)
        status = {"observed": 10, "pending": 0, "complete": True}
        with patch.object(load, "kubectl", return_value=""), patch.object(load, "collect", return_value=status), patch.object(load, "patch_objects", side_effect=RuntimeError("kubectl patch failed (exit 1)")):
            result = load.exercise("kind-ci", {"kind": "BitrixCluster"}, "run12345", 10, args)
        self.assertEqual(result["create"], status)
        self.assertIsNone(result["spec_update"])
        self.assertEqual(result["failure"]["phase"], "spec_update")

    def test_failed_kubectl_does_not_echo_payload(self):
        with patch.object(load.subprocess, "run", return_value=subprocess.CompletedProcess(
            ["kubectl"], 1, "secret", "password=value"
        )):
            with self.assertRaisesRegex(RuntimeError, "kubectl patch failed"):
                load.kubectl("kind-test", ["patch", "bitrixclusters"], {"password": "secret"})


if __name__ == "__main__":
    unittest.main()
