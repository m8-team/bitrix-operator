"""Record the actual build inputs; signed later with the uploaded image digest.

This is a workflow-authored SLSA v1 predicate, not a certified SLSA build level.
"""
import json
import os
import pathlib
import re

root = pathlib.Path(__file__).resolve().parents[2]
base_images = re.findall(r"^ARG (?:BASE_IMAGE|RUNTIME_IMAGE)=(.+)$", (root / "Dockerfile").read_text(), re.M)
source = "https://github.com/" + os.environ["GITHUB_REPOSITORY"]
sha = os.environ["GITHUB_SHA"]
workflow = source + "/.github/workflows/release.yml"
deps = [{"uri": "git+" + source, "digest": {"gitCommit": sha}}]
for image in base_images:
    name, digest = image.split("@sha256:")
    deps.append({"uri": "pkg:docker/" + name, "digest": {"sha256": digest}})
print(json.dumps({
    "buildDefinition": {
        "buildType": workflow + "#native-docker-build-v1",
        "externalParameters": {"platform": "linux/" + os.environ["ARCH"], "source": source, "revision": sha},
        "internalParameters": {"dockerConfigDigest": os.environ["IMAGE_ID"]},
        "resolvedDependencies": deps,
    },
    "runDetails": {
        "builder": {"id": workflow + "@" + os.environ["GITHUB_REF"]},
        "metadata": {"invocationId": source + "/actions/runs/" + os.environ["GITHUB_RUN_ID"]
                     + "/attempts/" + os.environ["GITHUB_RUN_ATTEMPT"]},
    },
}, indent=2))
