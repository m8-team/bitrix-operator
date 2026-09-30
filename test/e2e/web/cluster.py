"""Adapt the documented sample for a disposable kind cluster, preserving its API contract."""
import json
import sys

cluster = json.load(sys.stdin)
engine = sys.argv[1]
cluster["metadata"] = {"name": engine.lower(), "namespace": "bitrix-web-test"}
spec = cluster["spec"]
spec["release"]["image"] = {
    "repository": "bitrix-runtime-fixture", "tag": "e2e", "pullPolicy": "Never"
}
spec["database"]["engine"] = engine
connection = spec["database"]["connections"][0]
connection["host"] = "dependencies.bitrix-dependencies-test.svc.cluster.local"
connection["port"] = 3306 if engine == "MySQL" else 5432
spec["redis"]["host"] = connection["host"]
spec["storage"]["uploads"]["persistentVolumeClaim"] = {
    "storageClassName": "standard", "accessModes": ["ReadWriteOnce"], "size": "128Mi"
}
json.dump(cluster, sys.stdout)
