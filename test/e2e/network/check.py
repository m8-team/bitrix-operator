"""Real Service traffic; denied means repeated dial timeouts, not DNS/exec failure."""
import json
import subprocess
import sys
import time

from fixture import identities

ROLES = identities()


def kubectl(*args):
    return subprocess.check_output(["kubectl", *args], text=True)


def connect(source, address):
    namespace, _ = ROLES[source]
    return subprocess.run([
        "kubectl", "-n", namespace, "exec", f"deployment/{source}", "--",
        "/netprobe", "connect", address,
    ], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15).returncode


def expect(source, target, port, allowed, ips):
    desired = 0 if allowed else 3
    consecutive = 0
    # Allow policy/endpoint convergence, require three fresh denied connections.
    for _ in range(12):
        code = connect(source, f"{ips[target]}:{port}")
        if code not in (0, 3):
            raise AssertionError(f"fixture/exec failure {source} -> {target}:{port}, exit={code}")
        consecutive = consecutive + 1 if code == desired else 0
        if consecutive >= (1 if allowed else 3):
            print(f"PASS {'allow' if allowed else 'deny'} {source} -> {target}:{port}", flush=True)
            return
        time.sleep(1)
    raise AssertionError(f"wrong policy result {source} -> {target}:{port}, allowed={allowed}")


def matrix(db_port):
    wrong_port = 5432 if db_port == 3306 else 3306
    cases = [
        ("gateway", "web", 8080, True), ("gateway", "web", 8081, False),
        ("wrong-gateway-ns", "web", 8080, False), ("wrong-gateway-pod", "web", 8080, False),
        ("rogue", "web", 8080, False), ("gateway", "publisher", 9010, False),
        ("gateway", "subscriber", 8010, False), ("rogue", "database", db_port, False),
        ("web", "publisher", 9010, True), ("web", "subscriber", 8010, True),
        ("cron", "publisher", 9010, True), ("cron", "subscriber", 8010, False),
        ("sites", "publisher", 9010, False), ("web-b", "publisher-b", 9010, True),
        ("web", "publisher-b", 9010, False), ("web", "unrelated", 8443, False),
        ("prometheus", "manager", 8443, True), ("wrong-prometheus-ns", "manager", 8443, False),
        ("wrong-prometheus-pod", "manager", 8443, False), ("prometheus", "manager", 8081, False),
        ("manager", "unrelated", 8443, True),
    ]
    for role in ("web", "cron", "sites", "publisher", "subscriber"):
        cases.append((role, "redis", 6379, True))
        cases.append((role, "database", db_port, role in ("web", "cron", "sites")))
    for role in ("web", "cron", "sites"):
        cases.append((role, "database", wrong_port, False))
    return cases


def dns():
    address = kubectl("-n", "kube-system", "get", "service", "kube-dns", "-o", "jsonpath={.spec.clusterIP}") + ":53"
    for role in ("web", "cron", "sites", "publisher", "subscriber"):
        for transport in ("udp", "tcp"):
            kubectl("-n", ROLES[role][0], "exec", f"deployment/{role}", "--", "/netprobe", "dns", address, transport)
            print(f"PASS DNS {transport} from {role}", flush=True)


def web_pod():
    pods = json.loads(kubectl("-n", "bitrix-sites", "get", "pods", "-l", "test.bitrix.m8.team/fixture=web", "-o", "json"))["items"]
    assert len(pods) == 1, "expected one settled web Pod"
    return pods[0]


def main():
    mode, cluster = sys.argv[1:]
    ips = {name: kubectl("-n", namespace, "get", "service", name, "-o", "jsonpath={.spec.clusterIP}") for name, (namespace, _) in ROLES.items()}
    if mode == "preflight":
        # Every denial endpoint/port must be healthy and reachable before isolation.
        for source, target, port, _ in matrix(3306):
            expect(source, target, port, True, ips)
        dns()
        return
    assert mode == "enforce"
    for case in matrix(3306):
        expect(*case, ips)
    dns()
    print("PASS MySQL profile", flush=True)
    rendered = subprocess.check_output(["bin/kustomize", "build", "config/security/postgresql"])
    subprocess.run(["kubectl", "apply", "-f", "-"], input=rendered, check=True)
    for case in matrix(5432):
        if case[1] == "database":
            expect(*case, ips)
    print("PASS PostgreSQL profile switches allowed DB port", flush=True)
    before = web_pod()
    assert before["spec"]["nodeName"] == cluster + "-worker"
    patch = {"spec": {"template": {"spec": {"nodeSelector": {"kubernetes.io/hostname": cluster + "-control-plane"}}}}}
    kubectl("-n", "bitrix-sites", "patch", "deployment", "web", "--type=merge", "-p", json.dumps(patch))
    kubectl("-n", "bitrix-sites", "rollout", "status", "deployment/web", "--timeout=180s")
    after = web_pod()
    assert after["metadata"]["uid"] != before["metadata"]["uid"]
    assert after["spec"]["nodeName"] == cluster + "-control-plane"
    for case in matrix(5432):
        expect(*case, ips)
    dns()
    print("PASS policies after cross-node rollout", flush=True)
    kubectl("-n", "bitrix-sites", "delete", "pod", after["metadata"]["name"], "--wait=true")
    kubectl("-n", "bitrix-sites", "rollout", "status", "deployment/web", "--timeout=180s")
    recreated = web_pod()
    assert recreated["metadata"]["uid"] != after["metadata"]["uid"]
    for case in matrix(5432):
        if "web" in case[:2]:
            expect(*case, ips)
    dns()
    print("Network policy E2E passed: Cilium, Service flows, DNS TCP/UDP, UID isolation, DB profiles and cross-node recreation", flush=True)


if __name__ == "__main__":
    main()
