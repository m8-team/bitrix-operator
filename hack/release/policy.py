"""Fail closed before granting a release job registry/OIDC permissions."""
import json
import os
import re
import urllib.error
import urllib.parse
import urllib.request

WORKFLOWS = (
    "test.yml", "lint.yml", "runtime.yml", "web.yml", "network.yml",
    "observability.yml", "recovery.yml", "test-chart.yml", "release.yml",
)
TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc\.[1-9][0-9]*)?")


def validate_tag(tag):
    if not TAG.fullmatch(tag):
        raise ValueError("Release tag must be vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N")


def validate_runs(runs, sha):
    eligible = [r for r in runs if r.get("head_sha") == sha
                and r.get("event") == "push" and r.get("head_branch") == "main"]
    if not eligible:
        raise ValueError("No main push CI run for the release commit")
    latest = max(eligible, key=lambda r: (r["id"], r.get("run_attempt", 1)))
    if latest.get("status") != "completed" or latest.get("conclusion") != "success":
        raise ValueError("Latest main CI run is not successful")


def api(path, allow_missing=False):
    req = urllib.request.Request(
        "https://api.github.com/repos/" + os.environ["GITHUB_REPOSITORY"] + path,
        headers={"Authorization": "Bearer " + os.environ["GH_TOKEN"],
                 "Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"},
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:
        if allow_missing and exc.code == 404:
            return None
        raise


def main():
    tag, sha = os.environ["GITHUB_REF_NAME"], os.environ["GITHUB_SHA"]
    validate_tag(tag)
    if os.environ["GITHUB_REPOSITORY"] != "m8-team/bitrix-operator":
        raise ValueError("Publishing is restricted to the canonical repository")
    if api("/releases/tags/" + urllib.parse.quote(tag), allow_missing=True) is not None:
        raise ValueError("Release already exists; never overwrite it")
    # Only main HEAD is eligible, avoiding a new tag on an obsolete/security-fixed commit.
    if api("/commits/main")["sha"] != sha:
        raise ValueError("Release commit must equal current main HEAD")
    for workflow in WORKFLOWS:
        query = urllib.parse.urlencode({"head_sha": sha, "event": "push", "branch": "main", "per_page": 100})
        runs = api("/actions/workflows/" + workflow + "/runs?" + query)["workflow_runs"]
        validate_runs(runs, sha)
        print("PASS", workflow, sha)


if __name__ == "__main__":
    main()
