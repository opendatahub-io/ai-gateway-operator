#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Flat plain-YAML sources ship no kustomization; generate one (the render engine requires it).
ensure_kustomization() {
    local dir="$1"
    [[ -f "${dir}/kustomization.yaml" ]] && return
    [[ -n "$(find "${dir}" -mindepth 1 -maxdepth 1 -type d)" ]] && return

    local files=("${dir}"/*.yaml)
    [[ -e "${files[0]}" ]] || return 0
    {
        echo "apiVersion: kustomize.config.k8s.io/v1beta1"
        echo "kind: Kustomization"
        echo "resources:"
        local f
        for f in "${files[@]}"; do
            echo "  - $(basename "${f}")"
        done
    } > "${dir}/kustomization.yaml"
    echo "Generated ${dir}/kustomization.yaml"
}

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
        ensure_kustomization "${dst_manifests_dir}"
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
    ensure_kustomization "${dst_manifests_dir}"

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
    [batchgateway]="llm-d-batch-gateway-operator|config|1fdf57bc2bc5b6ab3cb3ebbc93d04d54e2531059"
    [maascontroller]="models-as-a-service|deployment/base/maas-controller|353a85e841d8442afc976a539e49e2925b73e017|353a85e841d8442afc976a539e49e2925b73e017"
    [aigatewaycontroller]="ai-gateway-controller|config/self|46caf7ee42c74e49ddbb2f1037895c0a89251a39|46caf7ee42c74e49ddbb2f1037895c0a89251a39"
    [aigrid]="praxis-ai-grid-operator|deploy/crds|0903bd8a05f06b6696b8da159576c3c881344907|1c12d5b53bf120d813b2d8bccd0f95dd6ea2f9fd"
)

for component_name in "${!COMPONENTS[@]}"; do
    IFS='|' read -r repo_name src_path odh_commit rhds_commit <<< "${COMPONENTS[$component_name]}"
    fetch_component "$component_name" "$repo_name" "$src_path" "$odh_commit" "$rhds_commit"
done
