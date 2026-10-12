import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("policy", pathlib.Path(__file__).resolve().parents[2] / "hack/release/policy.py")
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)


class ReleasePolicyTest(unittest.TestCase):
    def run_record(self, **overrides):
        return dict(dict(id=10, run_attempt=1, head_sha="abc", event="push", head_branch="main",
                         status="completed", conclusion="success"), **overrides)

    def test_stable_and_rc_tags(self):
        for tag in ["v0.1.0", "v12.3.45", "v1.0.0-rc.1"]:
            policy.validate_tag(tag)

    def test_malformed_tags(self):
        for tag in ["v1", "v01.2.3", "v1.2.3-rc.0", "v1.2.3+build", "v1.2.3\n", "main", "v1.2.3;id"]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                policy.validate_tag(tag)

    def test_green_main(self):
        policy.validate_runs([self.run_record()], "abc")

    def test_missing_wrong_commit_or_pr_is_not_release_evidence(self):
        for runs in [[], [self.run_record(head_sha="def")], [self.run_record(event="pull_request")],
                     [self.run_record(head_branch="feature")]]:
            with self.subTest(runs=runs), self.assertRaises(ValueError):
                policy.validate_runs(runs, "abc")

    def test_new_failure_or_in_progress_run_overrides_old_success(self):
        for override in [{"conclusion": "failure"}, {"conclusion": "cancelled"},
                         {"conclusion": None, "status": "in_progress"}, {"conclusion": "skipped"}]:
            with self.subTest(override=override), self.assertRaises(ValueError):
                policy.validate_runs([self.run_record(), self.run_record(id=11, **override)], "abc")

    def test_failed_rerun_overrides_success(self):
        with self.assertRaises(ValueError):
            policy.validate_runs([self.run_record(), self.run_record(run_attempt=2, conclusion="failure")], "abc")


if __name__ == "__main__":
    unittest.main()
