#!/usr/bin/env bash
set -euo pipefail
: "${GITHUB_REPOSITORY:?}" "${GITHUB_REF_NAME:?}" "${GITHUB_SHA:?}" "${GITHUB_RUN_ID:?}" "${GITHUB_RUN_ATTEMPT:?}"
python3 -c 'import sys; sys.path.insert(0, "hack/release"); from policy import validate_tag; import os; validate_tag(os.environ["GITHUB_REF_NAME"])'
image="ghcr.io/${GITHUB_REPOSITORY}"
tag=$GITHUB_REF_NAME
identity="https://github.com/${GITHUB_REPOSITORY}/.github/workflows/release.yml@refs/tags/${tag}"
verify=(--certificate-identity "$identity" --certificate-oidc-issuer https://token.actions.githubusercontent.com)
mkdir -p release-assets

# A reservation draft makes retries fail closed instead of overwriting a partial release.
# Version tags and the release environment must additionally be protected in repository settings.
gh release create "$tag" --verify-tag --draft --title "$tag" \
  --notes 'Release assembly in progress. Do not publish this draft until this workflow succeeds.'

refs=()
for arch in amd64 arm64; do
  directory="images/image-$arch"
  docker load -i "$directory/image.tar"
  actual_id=$(docker image inspect "bitrix-operator:ci-$arch" --format '{{.Id}}')
  expected_id=$(jq -r '.buildDefinition.internalParameters.dockerConfigDigest' "$directory/provenance.json")
  test "$actual_id" = "$expected_id"
  test "$(jq -r '.buildDefinition.externalParameters.revision' "$directory/provenance.json")" = "$GITHUB_SHA"
  test "$(jq -r '.buildDefinition.externalParameters.platform' "$directory/provenance.json")" = "linux/$arch"
  staging="$image:build-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT-$arch"
  docker tag "bitrix-operator:ci-$arch" "$staging"
  docker push "$staging"
  digest=$(docker buildx imagetools inspect "$staging" --format '{{json .Manifest}}' | jq -er '.digest')
  [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]
  ref="$image@$digest"
  refs+=("$ref")
  cosign sign --yes "$ref"
  cosign attest --yes --type spdxjson --predicate "$directory/sbom.spdx.json" "$ref"
  cosign attest --yes --type slsaprovenance1 --predicate "$directory/provenance.json" "$ref"
  cosign verify "${verify[@]}" "$ref" > /dev/null
  cosign verify-attestation "${verify[@]}" --type spdxjson "$ref" > /dev/null
  cosign verify-attestation "${verify[@]}" --type slsaprovenance1 "$ref" > /dev/null
  cp "$directory/sbom.spdx.json" "release-assets/sbom-linux-$arch.spdx.json"
  cp "$directory/provenance.json" "release-assets/provenance-linux-$arch.json"
  cp "$directory/vulnerabilities.json" "release-assets/vulnerabilities-linux-$arch.json"
done

candidate="$image:build-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT"
docker buildx imagetools create --tag "$candidate" "${refs[@]}"
docker buildx imagetools inspect "$candidate" --raw > release-assets/index.json
jq -e '[.manifests[].platform | .os + "/" + .architecture] | sort == ["linux/amd64", "linux/arm64"]' release-assets/index.json
index_digest=$(docker buildx imagetools inspect "$candidate" --format '{{json .Manifest}}' | jq -er '.digest')
[[ "$index_digest" =~ ^sha256:[0-9a-f]{64}$ ]]
ref="$image@$index_digest"
cosign sign --yes "$ref"
cosign verify "${verify[@]}" "$ref" > /dev/null
printf '%s\n' "$ref" > release-assets/image.txt

# Release manifests always refer to the verified immutable index, never a mutable tag.
make build-installer IMG="$ref"
cp dist/install.yaml release-assets/install.yaml
cp config/crd/bases/bitrix.m8.team_bitrixclusters.yaml release-assets/bitrixcluster-crd.yaml
bash hack/install-helm.sh
bin/helm package dist/chart --version "${tag#v}" --app-version "$tag" --destination release-assets
cp LICENSE release-assets/LICENSE
(cd release-assets && sha256sum ./* > ../release-SHA256SUMS)
mv release-SHA256SUMS release-assets/SHA256SUMS
cosign sign-blob --yes --bundle release-assets/SHA256SUMS.sigstore.json release-assets/SHA256SUMS
cosign verify-blob "${verify[@]}" --bundle release-assets/SHA256SUMS.sigstore.json release-assets/SHA256SUMS

# Refuse a pre-existing version alias. Registry errors must not be mistaken for absence.
# The release draft reservation above prevents normal repeated runs for this version.
if docker buildx imagetools inspect "$image:$tag" > /dev/null 2> release-tag-error.txt; then
  echo 'Version image already exists; refusing overwrite' >&2
  exit 1
elif ! grep -Eiq 'manifest unknown|not found' release-tag-error.txt; then
  cat release-tag-error.txt >&2
  exit 1
fi
docker buildx imagetools create --tag "$image:$tag" "$ref"
test "$(docker buildx imagetools inspect "$image:$tag" --format '{{json .Manifest}}' | jq -r '.digest')" = "$index_digest"

cat > release-notes.md <<NOTES
Manager image: \`$ref\`

Platforms: linux/amd64, linux/arm64. Both images passed native startup and Trivy HIGH/CRITICAL gates.
The image index and platform manifests are keyless-signed. Platform SBOMs and workflow-authored
SLSA v1 provenance are signed attestations. Verify the exact workflow identity and tag as described
in docs/releases.md. SHA256SUMS.sigstore.json authenticates the attached asset checksums.

install.yaml pins the image digest. The optional Helm chart still requires manager.image.reference.
CRD changes must be reviewed/applied separately on upgrade. A manager rollback does not undo
Bitrix migrations or database changes. No licensed Bitrix or production CSI certification is implied.

Source commit: $GITHUB_SHA
Workflow: https://github.com/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID
NOTES
gh release upload "$tag" release-assets/*
gh release edit "$tag" --notes-file release-notes.md
printf 'Draft release complete: %s; image: %s\n' "$tag" "$ref"
