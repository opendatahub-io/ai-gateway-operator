#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

COMPONENT_NAME="batchgateway"
REPO_NAME="llm-d-batch-gateway-operator"
SOURCE_PATH="config"
DST_MANIFESTS_DIR="${PROJECT_ROOT}/config/manifests/${COMPONENT_NAME}"

REPO_URL="https://github.com/opendatahub-io/${REPO_NAME}"
COMMIT_SHA="c426eeb4dc90e9ac694fa31ea20a7354c593a94e"

if [[ "${USE_LOCAL:-}" == "true" ]] && [[ -d "${PROJECT_ROOT}/../${REPO_NAME}" ]]; then
    echo "Copying manifests from adjacent ${REPO_NAME} checkout"
    rm -rf "${DST_MANIFESTS_DIR}"
    mkdir -p "${DST_MANIFESTS_DIR}"
    cp -a "${PROJECT_ROOT}/../${REPO_NAME}/${SOURCE_PATH}/." "${DST_MANIFESTS_DIR}/"
    echo "Manifests copied to ${DST_MANIFESTS_DIR}"
    exit 0
fi

TMP_DIR=$(mktemp -d -t "odh-batchgateway-manifests.XXXXXXXXXX")
trap 'rm -rf -- "${TMP_DIR}"' EXIT

git -C "${TMP_DIR}" init -q
git -C "${TMP_DIR}" remote add origin "${REPO_URL}"
git -C "${TMP_DIR}" fetch --depth 1 -q origin "${COMMIT_SHA}"
git -C "${TMP_DIR}" reset -q --hard FETCH_HEAD

rm -rf "${DST_MANIFESTS_DIR}"
mkdir -p "${DST_MANIFESTS_DIR}"
cp -a "${TMP_DIR}/${SOURCE_PATH}/." "${DST_MANIFESTS_DIR}/"

# TODO: remove once quay.io/opendatahub/odh-batch-gateway-operator is published
sed -i.bak 's|BATCH_GATEWAY_OPERATOR_IMAGE=.*|BATCH_GATEWAY_OPERATOR_IMAGE=ghcr.io/opendatahub-io/batch-gateway-operator:main|' \
    "${DST_MANIFESTS_DIR}/base/params.env"
rm -f "${DST_MANIFESTS_DIR}/base/params.env.bak"


echo "Manifests downloaded to ${DST_MANIFESTS_DIR}"
