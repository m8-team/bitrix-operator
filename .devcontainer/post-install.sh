#!/usr/bin/env bash
set -euo pipefail
bash hack/install-kubebuilder.sh /usr/local/bin
make controller-gen kustomize setup-envtest
go version
