"""Disposable logical restore drill; NOT a production backup/restore command."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time

SOURCE = "bitrix-web-test"
SOURCE_DB = "bitrix-dependencies-test"
TARGET = "bitrix-recovery-test"
TARGET_DB = "bitrix-recovery-data"
ENGINES = ("mysql", "postgresql")
FILES = {"secrets.json"} | {
    f"{engine}.{suffix}" for engine in ENGINES for suffix in ("json", "dump", "tar")
}
PAYLOAD = b"committed-before-backup\n"
DIGEST = hashlib.sha256(PAYLOAD).hexdigest()


def run(*args, data=None):
    result = subprocess.run(args, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=400)
    if result.returncode:
        # Do not include stdin, SQL/dump contents or Secret objects in failure output.
        raise RuntimeError(f"Command failed ({result.returncode}): {' '.join(args[:6])}")
    return result.stdout


def kubectl(namespace, *args, data=None):
    return run("kubectl", "-n", namespace, *args, data=data)


def get(namespace, kind, name=None):
    return json.loads(kubectl(namespace, "get", kind, *([name] if name else []), "-o", "json"))


def create(obj):
    # create deliberately refuses existing objects; never apply over a recovery target.
    run("kubectl", "create", "-f", "-", data=json.dumps(obj).encode())


def template(path):
    return json.loads(run("kubectl", "create", "--dry-run=client", "-f", path, "-o", "json"))


def execute(namespace, resource, container, *args, data=None):
    return kubectl(namespace, "exec", "-i", resource, "-c", container, "--", *args, data=data)


def web(namespace, engine, *args, data=None, container="php"):
    return execute(namespace, f"deployment/{engine}-web", container, *args, data=data)


def database(namespace, engine, operation, data=None):
    if engine == "mysql":
        commands = {
            "sql": 'MYSQL_PWD="$MYSQL_PASSWORD" mysql -h 127.0.0.1 -u bitrix --batch --skip-column-names bitrix',
            "dump": 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump -h 127.0.0.1 -u root '
                    '--single-transaction --quick --no-tablespaces --set-gtid-purged=OFF '
                    '--routines --events --triggers bitrix',
        }
        command = commands["sql" if operation == "restore" else operation]
        container = "mysql"
    else:
        commands = {
            "sql": "psql -X -U bitrix -d bitrix -At -v ON_ERROR_STOP=1",
            "dump": "pg_dump -U bitrix -d bitrix --format=custom --no-owner --no-acl",
            "restore": "pg_restore -U bitrix -d bitrix --exit-on-error --single-transaction --no-owner --no-acl",
        }
        command = commands[operation]
        container = "postgres"
    return execute(namespace, "deployment/dependencies", container, "sh", "-ec", command, data=data)


def sql(namespace, engine, query):
    return database(namespace, engine, "sql", query.encode()).decode().strip()


def verify_bundle(directory):
    manifest = json.loads((directory / "manifest.json").read_text())
    if manifest.get("format") != 1 or set(manifest.get("sha256", {})) != FILES:
        raise ValueError("Incomplete or unexpected backup file set")
    for name, expected in manifest["sha256"].items():
        path = directory / name
        if path.is_symlink() or not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != expected:
            raise ValueError(f"Backup integrity failure: {name}")
    return manifest


def seal_bundle(directory, metadata):
    manifest = {"format": 1, **metadata, "sha256": {
        name: hashlib.sha256((directory / name).read_bytes()).hexdigest() for name in sorted(FILES)
    }}
    (directory / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    verify_bundle(directory)


def wait_condition(namespace, engine, condition):
    kubectl(namespace, "wait", f"bitrixcluster/{engine}", f"--for={condition}", "--timeout=300s")


def no_application_pods():
    pods = get(TARGET, "pods")["items"]
    if any(p["metadata"].get("labels", {}).get("bitrix.m8.team/owner-uid") for p in pods):
        raise RuntimeError("Application started before restore completed")
    if get(TARGET, "deployments,jobs")["items"]:
        raise RuntimeError("Application resources created before Secrets were released")


def main(cluster_name, directory):
    if not cluster_name.startswith("bx-restore-"):
        raise ValueError("This drill only runs in its own disposable kind cluster")
    if run("kubectl", "config", "current-context").decode().strip() != f"kind-{cluster_name}":
        raise ValueError("Unexpected Kubernetes context")
    if not os.environ.get("KUBECONFIG") or not directory.is_dir():
        raise ValueError("Private kubeconfig and temporary backup directory are required")
    os.umask(0o077)
    sources = {}
    source_pvcs = {}
    source_uids = {}
    for engine in ENGINES:
        live = get(SOURCE, "bitrixcluster", engine)
        source_uids[engine] = live["metadata"]["uid"]
        sources[engine] = {k: live[k] for k in ("apiVersion", "kind", "spec")}
        sources[engine]["metadata"] = {"name": engine, "namespace": SOURCE}
        source_pvcs[engine] = get(SOURCE, "pvc", f"{engine}-uploads")["metadata"]["uid"]
        (directory / f"{engine}.json").write_text(json.dumps(sources[engine]))
        # The fixture has no user writes, cron, Push, Gateway or DB event writers.
        sql(SOURCE_DB, engine, "CREATE TABLE recovery_marker (id INTEGER PRIMARY KEY, digest VARCHAR(64) NOT NULL);")
        web(SOURCE, engine, "sh", "-ec", "cat > /opt/www/upload/checkpoint.txt", data=PAYLOAD)
        sql(SOURCE_DB, engine, f"INSERT INTO recovery_marker VALUES (1, '{DIGEST}');")
        assert sql(SOURCE_DB, engine, "SELECT digest FROM recovery_marker WHERE id=1;") == DIGEST
    secrets = []
    for name in ("bitrix-license", "bitrix-crypto", "bitrix-database", "bitrix-redis"):
        live = get(SOURCE, "secret", name)
        secrets.append({"apiVersion": "v1", "kind": "Secret", "metadata": {"name": name},
                        "type": live["type"], "data": live["data"]})
    (directory / "secrets.json").write_text(json.dumps(secrets))
    checkpoint = time.time()
    for engine in ENGINES:
        (directory / f"{engine}.dump").write_bytes(database(SOURCE_DB, engine, "dump"))
        (directory / f"{engine}.tar").write_bytes(web(SOURCE, engine, "tar", "-C", "/opt/www/upload", "-cf", "-", "."))
    seal_bundle(directory, {"checkpoint_unix": checkpoint, "source_pvc_uids": source_pvcs,
                            "source_cr_uids": source_uids})
    # Exercise the actual pre-restore integrity gate against a corrupt uploads archive.
    path = directory / "mysql.tar"
    original = path.read_bytes()
    path.write_bytes(original + b"corrupted")
    try:
        verify_bundle(directory)
    except ValueError:
        print("PASS corrupt archive rejected before restore", flush=True)
    else:
        raise RuntimeError("Corrupt backup accepted")
    path.write_bytes(original)
    for engine in ENGINES:
        sql(SOURCE_DB, engine, f"INSERT INTO recovery_marker VALUES (2, '{DIGEST}');")
        web(SOURCE, engine, "sh", "-ec", "echo after-backup > /opt/www/upload/after-backup.txt")
        assert sql(SOURCE_DB, engine, "SELECT COUNT(*) FROM recovery_marker;") == "2"
    incident = time.time()
    started = time.monotonic()
    # Make the original application and DB endpoints unavailable; retain source PVCs.
    for engine in ENGINES:
        kubectl(SOURCE, "delete", f"bitrixcluster/{engine}")
        kubectl(SOURCE, "wait", f"deployment/{engine}-web", "--for=delete", "--timeout=180s")
    kubectl(SOURCE_DB, "scale", "deployment/dependencies", "--replicas=0")
    kubectl(SOURCE_DB, "wait", "pods", "-l", "app=external-dependencies", "--for=delete", "--timeout=120s")
    for engine in ENGINES:
        assert get(SOURCE, "pvc", f"{engine}-uploads")["metadata"]["uid"] == source_pvcs[engine]
    create(sources["mysql"])
    wait_condition(SOURCE, "mysql", "jsonpath={.status.conditions[?(@.type==\"Ready\")].reason}=StorageCollision")
    print("PASS retained source PVC refuses a new CR UID", flush=True)
    verify_bundle(directory)
    for namespace in (TARGET, TARGET_DB):
        create({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": namespace}})
    kubectl(TARGET, "label", "namespace", TARGET, "pod-security.kubernetes.io/enforce=restricted")
    # Same pinned dependency manifests, but fresh database processes and empty data directories.
    dependencies = template("test/e2e/web/dependencies.yaml")
    for obj in dependencies["items"]:
        if obj["kind"] != "Namespace":
            obj["metadata"]["namespace"] = TARGET_DB
            create(obj)
    kubectl(TARGET_DB, "rollout", "status", "deployment/dependencies", "--timeout=300s")
    for engine in ENGINES:
        assert sql(TARGET_DB, engine, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name='recovery_marker';") == "0"
        # Load the backed-up spec, keeping the original name but assigning a fresh namespace/UID.
        restored = json.loads((directory / f"{engine}.json").read_text())
        restored["metadata"]["namespace"] = TARGET
        restored["spec"]["database"]["connections"][0]["host"] = f"dependencies.{TARGET_DB}.svc.cluster.local"
        restored["spec"]["redis"]["host"] = f"dependencies.{TARGET_DB}.svc.cluster.local"
        create(restored)
        wait_condition(TARGET, engine, "jsonpath={.status.conditions[?(@.type==\"Ready\")].reason}=SecretMissing")
        no_application_pods()
        owner = get(TARGET, "bitrixcluster", engine)["metadata"]["uid"]
        pvc = template("test/e2e/recovery/uploads.yaml")
        pvc["metadata"]["name"] = f"{engine}-uploads"
        pvc["metadata"]["namespace"] = TARGET
        pvc["metadata"]["labels"]["bitrix.m8.team/owner-uid"] = owner
        pvc["metadata"]["annotations"]["bitrix.m8.team/cluster-name"] = engine
        create(pvc)
        helper = template("test/e2e/recovery/restore-pod.yaml")
        helper["metadata"] = {"name": f"restore-{engine}", "namespace": TARGET}
        helper["spec"]["containers"][0]["image"] = restored["spec"]["runtime"]["web"]["php"]["image"]
        helper["spec"]["volumes"][0]["persistentVolumeClaim"]["claimName"] = f"{engine}-uploads"
        create(helper)
        resource = f"pod/restore-{engine}"
        kubectl(TARGET, "wait", resource, "--for=condition=Ready", "--timeout=180s")
        verify_bundle(directory)
        database(TARGET_DB, engine, "restore", (directory / f"{engine}.dump").read_bytes())
        execute(TARGET, resource, "restore", "tar", "-C", "/restore", "-xf", "-", data=(directory / f"{engine}.tar").read_bytes())
        assert execute(TARGET, resource, "restore", "cat", "/restore/checkpoint.txt") == PAYLOAD
        execute(TARGET, resource, "restore", "test", "!", "-e", "/restore/after-backup.txt")
        assert sql(TARGET_DB, engine, "SELECT COUNT(*) FROM recovery_marker;") == "1"
        assert sql(TARGET_DB, engine, "SELECT digest FROM recovery_marker WHERE id=1;") == DIGEST
        assert get(TARGET, "pvc", f"{engine}-uploads")["metadata"]["uid"] != source_pvcs[engine]
        kubectl(TARGET, "delete", resource, "--wait=true")
    no_application_pods()
    # Release the startup gate only after BOTH DBs and files are restored and helpers removed.
    verify_bundle(directory)
    for secret in json.loads((directory / "secrets.json").read_text()):
        secret["metadata"]["namespace"] = TARGET
        create(secret)
        assert get(TARGET, "secret", secret["metadata"]["name"])["data"] == secret["data"]
    for engine in ENGINES:
        wait_condition(TARGET, engine, "condition=Ready")
        response = web(TARGET, engine, "wget", "-qO-", "--header=Host: portal.example.com", f"http://{engine}-web/", container="nginx")
        assert response == b"runtime-fixture"
        restored_bytes = web(TARGET, engine, "cat", "/opt/www/upload/checkpoint.txt")
        assert hashlib.sha256(restored_bytes).hexdigest() == sql(TARGET_DB, engine, "SELECT digest FROM recovery_marker WHERE id=1;")
        web(TARGET, engine, "sh", "-ec", "test ! -e /opt/www/upload/after-backup.txt; echo writable > /opt/www/upload/restore-write-test; rm /opt/www/upload/restore-write-test")
        assert web(TARGET, engine, "stat", "-c", "%u:%g", "/opt/www/upload/checkpoint.txt").strip() == b"979:979"
        print(f"PASS {engine}: independent DB/PVC, checkpoint restored, post-backup marker absent, HTTP ready", flush=True)
    report = {"result": "passed", "engines": list(ENGINES), "storage": "kind standard/local-path (not CSI)",
              "checkpoint_unix": checkpoint, "incident_unix": incident,
              "recovery_point_age_seconds": round(incident - checkpoint, 3),
              "restore_elapsed_seconds": round(time.monotonic() - started, 3),
              "intentionally_unrecovered_markers_per_engine": 1}
    print("RECOVERY_REPORT " + json.dumps(report), flush=True)
    print("Recovery drill passed; production CSI and licensed Bitrix restore are not certified", flush=True)


if __name__ == "__main__":
    main(sys.argv[1], Path(sys.argv[2]))
