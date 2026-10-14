#!/usr/bin/env bash
# Build and push ai-gateway-controller for E2E when vendored manifests enable
# flags (e.g. --metrics-secure) not yet present in odh-stable.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <image>" >&2
  exit 1
fi
IMG="$1"

line="$(grep '^\s*\[aigatewaycontroller\]=' "${SCRIPT_DIR}/get-manifests.sh")"
payload="${line#*=\"}"
payload="${payload%\"}"
IFS='|' read -r repo_name src_path odh_commit _ <<< "${payload}"

if [[ -z "${odh_commit}" ]]; then
  echo "failed to parse aigatewaycontroller commit from get-manifests.sh" >&2
  exit 1
fi

tmp_dir="$(mktemp -d -t agc-e2e-build.XXXXXXXXXX)"
trap 'rm -rf "${tmp_dir}"' EXIT

git -C "${tmp_dir}" init -q
git -C "${tmp_dir}" remote add origin "https://github.com/opendatahub-io/${repo_name}"
git -C "${tmp_dir}" fetch --depth 1 -q origin "${odh_commit}"
git -C "${tmp_dir}" reset -q --hard FETCH_HEAD

make -C "${tmp_dir}" get-manifests
docker build -f "${tmp_dir}/Dockerfile" --build-arg VERSION=e2e -t "${IMG}" "${tmp_dir}"
docker push "${IMG}"

echo "Built and pushed ${IMG} (commit ${odh_commit})"
