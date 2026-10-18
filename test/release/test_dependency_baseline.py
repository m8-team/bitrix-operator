"""Security patch-level floors for the operator build and its Go dependencies."""

import pathlib
import re
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]


class DependencySecurityBaselineTest(unittest.TestCase):
    def test_go_version_matches_pinned_builder(self):
        go_mod = (ROOT / "go.mod").read_text()
        dockerfile = (ROOT / "Dockerfile").read_text()
        module_go = re.search(r"(?m)^go (\d+)\.(\d+)\.(\d+)$", go_mod)
        builder_go = re.search(
            r"(?m)^ARG BASE_IMAGE=golang:(\d+)\.(\d+)\.(\d+)(?:-[\w.]+)?@sha256:([0-9a-f]{64})$",
            dockerfile,
        )
        self.assertIsNotNone(module_go)
        self.assertIsNotNone(builder_go)
        version = tuple(map(int, module_go.groups()))
        self.assertGreaterEqual(version, (1, 26, 9))
        self.assertEqual(version, tuple(map(int, builder_go.groups()[:3])))

    def test_x_net_at_least_0_61_0(self):
        go_mod = (ROOT / "go.mod").read_text()
        dependency = re.search(
            r"(?m)^\s*golang\.org/x/net v(\d+)\.(\d+)\.(\d+)\b", go_mod
        )
        self.assertIsNotNone(dependency)
        self.assertGreaterEqual(tuple(map(int, dependency.groups())), (0, 61, 0))


if __name__ == "__main__":
    unittest.main()
