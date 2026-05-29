#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# ---------------------------------------------------------------------------
# Component definitions: name|repo|commit_sha|source_path|post_hook
#
# To add a new sub-component, append a line to COMPONENTS and optionally
# define a post_hook_<name> function below.
# ---------------------------------------------------------------------------
COMPONENTS=(
    "batchgateway|llm-d-batch-gateway-operator|c426eeb4dc90e9ac694fa31ea20a7354c593a94e|config|post_hook_batchgateway"
)

# ---------------------------------------------------------------------------
# Post-download hooks
# ---------------------------------------------------------------------------

# TODO: remove once quay.io/opendatahub/odh-batch-gateway-operator is published
post_hook_batchgateway() {
    local dst="$1"
    sed -i.bak 's|BATCH_GATEWAY_OPERATOR_IMAGE=.*|BATCH_GATEWAY_OPERATOR_IMAGE=ghcr.io/opendatahub-io/batch-gateway-operator:main|' \
        "${dst}/base/params.env"
    rm -f "${dst}/base/params.env.bak"
}

# ---------------------------------------------------------------------------
# Fetch logic
# ---------------------------------------------------------------------------

fetch_component() {
    local name="$1" repo="$2" commit="$3" src_path="$4" hook="$5"
    local repo_url="https://github.com/opendatahub-io/${repo}"
    local dst="${PROJECT_ROOT}/config/manifests/${name}"

    if [[ "${USE_LOCAL:-}" == "true" ]] && [[ -d "${PROJECT_ROOT}/../${repo}" ]]; then
        echo "[${name}] Copying manifests from adjacent ${repo} checkout"
        rm -rf "${dst}"
        mkdir -p "${dst}"
        cp -a "${PROJECT_ROOT}/../${repo}/${src_path}/." "${dst}/"
    else
        echo "[${name}] Fetching ${repo}@${commit:0:7}"
        local tmp
        tmp=$(mktemp -d -t "odh-${name}-manifests.XXXXXXXXXX")

        git -C "${tmp}" init -q
        git -C "${tmp}" remote add origin "${repo_url}"
        git -C "${tmp}" fetch --depth 1 -q origin "${commit}"
        git -C "${tmp}" reset -q --hard FETCH_HEAD

        rm -rf "${dst}"
        mkdir -p "${dst}"
        cp -a "${tmp}/${src_path}/." "${dst}/"
        rm -rf "${tmp}"
    fi

    if [[ -n "${hook}" ]] && declare -f "${hook}" > /dev/null; then
        "${hook}" "${dst}"
    fi

    echo "[${name}] Manifests ready at ${dst}"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

for entry in "${COMPONENTS[@]}"; do
    IFS='|' read -r name repo commit src_path hook <<< "${entry}"
    fetch_component "${name}" "${repo}" "${commit}" "${src_path}" "${hook}"
done
