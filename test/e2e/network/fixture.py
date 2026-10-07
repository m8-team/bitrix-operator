"""Synthetic identities for dataplane tests, not application configuration."""
import copy
import json
import sys


def identities():
    app = "app.kubernetes.io/name"
    owner = "bitrix.m8.team/owner-uid"
    roles = {
        "web": "bitrix", "cron": "bitrix-cron", "sites": "bitrix-sites",
        "publisher": "bitrix-push-publisher", "subscriber": "bitrix-push-subscriber",
    }
    result = {name: ("bitrix-sites", {app: role, owner: "network-a"}) for name, role in roles.items()}
    result.update({
        "web-b": ("bitrix-sites", {app: "bitrix", owner: "network-b"}),
        "publisher-b": ("bitrix-sites", {app: "bitrix-push-publisher", owner: "network-b"}),
        "rogue": ("bitrix-sites", {app: "rogue"}),
        "database": ("bitrix-data", {app: "bitrix-database"}),
        "redis": ("bitrix-data", {app: "bitrix-redis"}),
        "gateway": ("gateway-system", {"bitrix.m8.team/traffic-role": "gateway"}),
        "wrong-gateway-ns": ("other", {"bitrix.m8.team/traffic-role": "gateway"}),
        "wrong-gateway-pod": ("gateway-system", {app: "rogue"}),
        "manager": ("bitrix-operator-system", {app: "bitrix-operator", "control-plane": "controller-manager"}),
        "prometheus": ("monitoring", {app: "prometheus"}),
        "wrong-prometheus-ns": ("other", {app: "prometheus"}),
        "wrong-prometheus-pod": ("monitoring", {app: "rogue"}),
        "unrelated": ("other", {app: "unrelated"}),
    })
    return result


def render(template, cluster):
    objects = []
    for name, (namespace, labels) in identities().items():
        selector = {"test.bitrix.m8.team/fixture": name}
        deployment = copy.deepcopy(template)
        deployment["metadata"] = {"name": name, "namespace": namespace, "labels": selector}
        spec = deployment["spec"]
        spec["selector"]["matchLabels"] = selector
        spec["template"]["metadata"]["labels"] = dict(labels, **selector)
        # DB/Gateway/manager on control-plane; application clients on worker.
        node = "control-plane" if name in {"database", "redis", "gateway", "manager", "unrelated"} else "worker"
        spec["template"]["spec"]["nodeSelector"] = {"kubernetes.io/hostname": f"{cluster}-{node}"}
        objects.append(deployment)
        objects.append({
            "apiVersion": "v1", "kind": "Service",
            "metadata": {"name": name, "namespace": namespace},
            "spec": {"selector": selector, "ports": [
                {"name": f"tcp-{port}", "port": port, "targetPort": port, "protocol": "TCP"}
                for port in [8080, 8081, 3306, 5432, 6379, 8010, 9010, 8443]
            ]},
        })
    return {"apiVersion": "v1", "kind": "List", "items": objects}


if __name__ == "__main__":
    json.dump(render(json.load(sys.stdin), sys.argv[1]), sys.stdout)
