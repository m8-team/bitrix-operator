#!/usr/bin/env bash
set -euo pipefail
# Intentionally pinned. Update version and checksums together after release review.
version=v4.16.0
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) arch=amd64; checksum=539984e58832044f2326b1bb940c1eaf784f1849e65d06d3a4a4a8b5dfac7ca7 ;;
  Linux/aarch64|Linux/arm64) arch=arm64; checksum=6833432c8faab62d2ca6b583f32c88b0a3d1b7264562739c202084c996a55845 ;;
  *) echo 'This helper supports Linux amd64/arm64. See upstream releases for other platforms.' >&2; exit 1 ;;
esac
install_dir="${1:-bin}"
mkdir -p "$install_dir"
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
curl --fail --location --retry 3 --max-time 180 \
  "https://github.com/kubernetes-sigs/kubebuilder/releases/download/${version}/kubebuilder_linux_${arch}" -o "$tmp"
printf '%s  %s\n' "$checksum" "$tmp" | sha256sum --check -
install -m 0755 "$tmp" "$install_dir/kubebuilder"
"$install_dir/kubebuilder" version
