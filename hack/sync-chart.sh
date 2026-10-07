#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
mkdir -p dist/chart/crds dist/chart/files
cp config/crd/bases/bitrix.m8.team_bitrixclusters.yaml dist/chart/crds/bitrix.m8.team_bitrixclusters.yaml
cp config/rbac/role.yaml dist/chart/files/manager-role.yaml
