#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

fetch_component() {
    local component_name="$1"
    local repo_name="$2"
    local src_path="$3"
    local odh_commit="$4"
    local rhds_commit="$5"

    local dst_manifests_dir="${PROJECT_ROOT}/config/manifests/${component_name}"

    # Always wipe the component dir before copy — see manifest-script.md in
    # .agents/skills/odh-component-to-module/references/

    local repo_url
    local commit_sha
    if [[ "${ODH_PLATFORM_TYPE:-OpenDataHub}" == "OpenDataHub" ]]; then
        echo "Downloading manifests for ODH"
        repo_url="https://github.com/opendatahub-io/${repo_name}"
        commit_sha="$odh_commit"
    else
        echo "Downloading manifests for RHDS"
        repo_url="https://github.com/red-hat-data-services/${repo_name}"
        commit_sha="$rhds_commit"
    fi

    if [[ -z "${commit_sha}" ]]; then
        echo "[ERROR] No commit SHA for ${component_name} (platform: ${ODH_PLATFORM_TYPE:-OpenDataHub})" >&2
        return 1
    fi

    if [[ "${USE_LOCAL:-}" == "true" ]] && [[ -d "${PROJECT_ROOT}/../${repo_name}" ]]; then
        echo "Copying manifests from adjacent ${repo_name} checkout"
        rm -rf "${dst_manifests_dir}"
        mkdir -p "${dst_manifests_dir}"
        cp -a "${PROJECT_ROOT}/../${repo_name}/${src_path}/." "${dst_manifests_dir}/"
        echo "Manifests copied to ${dst_manifests_dir}"
        return
    fi

    local tmp_dir=$(mktemp -d -t "odh-${component_name}-manifests.XXXXXXXXXX")

    git -C "${tmp_dir}" init -q
    git -C "${tmp_dir}" remote add origin "${repo_url}"
    git -C "${tmp_dir}" fetch --depth 1 -q origin "${commit_sha}"
    git -C "${tmp_dir}" reset -q --hard "${commit_sha}"

    rm -rf "${dst_manifests_dir}"
    mkdir -p "${dst_manifests_dir}"
    cp -a "${tmp_dir}/${src_path}/." "${dst_manifests_dir}/"

    rm -rf "${tmp_dir}"

    echo "[${component_name}] Manifests ready at ${dst_manifests_dir}"
}

# Component manifest sources. To upgrade a component, change its SHA(s) below
# and run: make get-manifests. To add a component, add a new entry.
#
#   <component_name> = "<repo_name>|<src_path>|<odh_commit>|<rhds_commit>"
#
# ODH commits:   https://github.com/opendatahub-io/<repo_name>/commits/
# RHDS commits: https://github.com/red-hat-data-services/<repo_name>/commits/

declare -A COMPONENTS=(
    [batchgateway]="llm-d-batch-gateway-operator|config|96266f4b7d9d8b4fbe56225a2b37e064717cd3e1|3ffb67bc46bc00e2183df4f568c92d621cdc1743"
    [maascontroller]="models-as-a-service|deployment/base/maas-controller|fbe914c4147d79217de034fa2d02ef819ea9681a|fbe914c4147d79217de034fa2d02ef819ea9681a"
)

for component_name in "${!COMPONENTS[@]}"; do
    IFS='|' read -r repo_name src_path odh_commit rhds_commit <<< "${COMPONENTS[$component_name]}"
    fetch_component "$component_name" "$repo_name" "$src_path" "$odh_commit" "$rhds_commit"
done