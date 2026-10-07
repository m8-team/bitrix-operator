#!/usr/bin/env bash
set -Eeuo pipefail
version=v4.3.0
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) arch=amd64; checksum=86584a54def73570558f66f5111cc53dfed56689637ae32c1201205d494f54fb ;;
  Linux/aarch64|Linux/arm64) arch=arm64; checksum=31c5794dd55c66a51e6b7d2e2ac7a114ae8b1de41ff1d9ba51748ac973b06a08 ;;
  *) echo 'This helper supports Linux amd64/arm64' >&2; exit 1 ;;
esac
install_dir="${1:-bin}"
helm_tmp=$(mktemp -d)
trap 'rm -rf -- "$helm_tmp"' EXIT
curl -fsSL --retry 3 --max-time 180 "https://get.helm.sh/helm-$version-linux-$arch.tar.gz" -o "$helm_tmp/helm.tgz"
printf '%s  %s\n' "$checksum" "$helm_tmp/helm.tgz" | sha256sum -c -
tar --no-same-owner -xzf "$helm_tmp/helm.tgz" -C "$helm_tmp" "linux-$arch/helm"
mkdir -p "$install_dir"
install -m 0755 "$helm_tmp/linux-$arch/helm" "$install_dir/helm"
"$install_dir/helm" version --short
