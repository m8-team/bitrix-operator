"""Fail-closed checks before any DB import or archive extraction."""
import json
from pathlib import Path
import tempfile
import unittest

from drill import FILES, decode_objects, seal_bundle, verify_bundle


class ManifestTest(unittest.TestCase):
    def test_kubectl_json_stream(self):
        namespace = {"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": "fixture"}}
        service = {"apiVersion": "v1", "kind": "Service", "metadata": {"name": "dependencies"}}
        stream = json.dumps(namespace) + "\n" + json.dumps(service) + "\n"
        self.assertEqual(decode_objects(stream)["items"], [namespace, service])
        self.assertEqual(decode_objects(json.dumps(service)), service)

    def test_empty_or_truncated_manifest(self):
        for stream in ("", '{"kind":'):
            with self.assertRaises(ValueError):
                decode_objects(stream)


class BackupIntegrityTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        for name in FILES:
            (self.directory / name).write_bytes(b"synthetic backup content")
        seal_bundle(self.directory, {"checkpoint_unix": 1})

    def test_complete_bundle(self):
        self.assertEqual(verify_bundle(self.directory)["checkpoint_unix"], 1)

    def test_corrupt_db_uploads_or_keys(self):
        for name in ("mysql.dump", "postgresql.dump", "mysql.tar", "secrets.json"):
            with self.subTest(name=name):
                path = self.directory / name
                original = path.read_bytes()
                path.write_bytes(original[:-1])
                with self.assertRaises(ValueError):
                    verify_bundle(self.directory)
                path.write_bytes(original)

    def test_missing_file(self):
        (self.directory / "secrets.json").unlink()
        with self.assertRaises(ValueError):
            verify_bundle(self.directory)

    def test_incomplete_or_path_traversal_manifest(self):
        path = self.directory / "manifest.json"
        original = json.loads(path.read_text())
        for name in ("mysql.tar", "../outside"):
            manifest = json.loads(json.dumps(original))
            if name in manifest["sha256"]:
                del manifest["sha256"][name]
            else:
                manifest["sha256"][name] = "ignored"
            path.write_text(json.dumps(manifest))
            with self.assertRaises(ValueError):
                verify_bundle(self.directory)

    def test_symlink_is_not_a_backup(self):
        path = self.directory / "mysql.tar"
        path.unlink()
        path.symlink_to(self.directory / "postgresql.tar")
        with self.assertRaises(ValueError):
            verify_bundle(self.directory)


if __name__ == "__main__":
    unittest.main()
