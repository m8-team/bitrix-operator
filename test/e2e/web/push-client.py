"""Disposable backend client using the real Push image/config and cron network identity."""
import json
import sys

source = json.load(sys.stdin)
template = source["spec"]["template"]
spec = template["spec"]
container = spec["containers"][0]
container["name"] = "probe"
container["command"] = ["/bin/sh", "-ec", "node /run/push-config/configure.cjs publisher; exec sleep 3600"]
for key in ("ports", "livenessProbe", "startupProbe"):
    container.pop(key, None)
container["readinessProbe"] = {
    "exec": {"command": ["/bin/sh", "-ec", "test -s /tmp/push/config.json"]},
    "periodSeconds": 1,
}
spec["restartPolicy"] = "Never"
print(json.dumps({
    "apiVersion": "v1", "kind": "Pod",
    "metadata": {
        "name": "postgresql-push-client", "namespace": source["metadata"]["namespace"],
        # Publisher explicitly allows cron, whose label does not match the web Service.
        "labels": {**template["metadata"]["labels"], "app.kubernetes.io/name": "bitrix-cron"},
    },
    "spec": spec,
}))
