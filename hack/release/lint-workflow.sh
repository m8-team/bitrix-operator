#!/usr/bin/env bash
set -euo pipefail
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl --retry 3 --connect-timeout 15 --max-time 180 -fsSL \
  https://github.com/rhysd/actionlint/releases/download/v1.7.12/actionlint_1.7.12_linux_amd64.tar.gz -o "$tmp/actionlint.tgz"
echo "8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8  $tmp/actionlint.tgz" | sha256sum -c -
tar --no-same-owner -xzf "$tmp/actionlint.tgz" -C "$tmp" actionlint
"$tmp/actionlint" -shellcheck='' .github/workflows/release.yml
for script in hack/release/*.sh; do bash -n "$script"; done
