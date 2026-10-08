#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
mkdir -p "$root/bin"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
case "$(uname -m)" in
  x86_64) arch=64bit; sha=c6e65abddb348e25f10549df887045629cf28cc72453cd1c63acb717316b3f3f ;;
  aarch64|arm64) arch=ARM64; sha=a1ee9f6ffb7d112b64ff726a2a0717c21175c1114361391f4a132956751a13b3 ;;
  *) echo 'Unsupported Trivy architecture' >&2; exit 1 ;;
esac
curl --retry 3 --connect-timeout 15 --max-time 180 -fsSL \
  "https://github.com/aquasecurity/trivy/releases/download/v0.75.0/trivy_0.75.0_Linux-${arch}.tar.gz" -o "$tmp/trivy.tgz"
echo "$sha  $tmp/trivy.tgz" | sha256sum -c -
tar --no-same-owner -xzf "$tmp/trivy.tgz" -C "$tmp" trivy
install -m 0755 "$tmp/trivy" "$root/bin/trivy"
"$root/bin/trivy" --version
