#!/usr/bin/env bash
# Render with a caller-selected image without changing the source Kustomize tree.
set -Eeuo pipefail
cd "$(dirname "$0")/.."
: "${IMG:?IMG must contain a manager image reference}"
: "${KUSTOMIZE:=$PWD/bin/kustomize}"
KUSTOMIZE=$(realpath "$KUSTOMIZE")
build_tmp=$(mktemp -d)
trap 'rm -rf -- "$build_tmp"' EXIT
cp -R config "$build_tmp/config"
(cd "$build_tmp/config/manager" && "$KUSTOMIZE" edit set image "controller=$IMG")
"$KUSTOMIZE" build "$build_tmp/config/default" > "$build_tmp/install.yaml"
mkdir -p dist
cp "$build_tmp/install.yaml" dist/install.yaml
