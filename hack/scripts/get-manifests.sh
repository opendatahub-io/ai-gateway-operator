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

copy_component_manifests() {
    local component_name="$1" src="$2" dst="$3"

    rm -rf "${dst}"
    mkdir -p "${dst}"
    if [[ "${component_name}" == "aigrid" ]]; then
        mkdir -p "${dst}/overlays"
        cp -a "${src}/crds" "${dst}/crds"
        cp -a "${src}/overlays/openshift" "${dst}/overlays/openshift"
        # Helm values generate these manifests upstream; Kustomize does not consume them.
        rm -f "${dst}/overlays/openshift/values.yaml"
        ensure_kustomization "${dst}/crds"
    else
        cp -a "${src}/." "${dst}/"
        ensure_kustomization "${dst}"
    fi
}

fetch_component() {
    local component_name="$1"
    local repo_name="$2"
    local src_path="$3"
    local odh_ref="$4"
    local rhds_ref="$5"

    local dst_manifests_dir="${PROJECT_ROOT}/config/manifests/${component_name}"

    # Always wipe the component dir before copy — see manifest-script.md in
    # .agents/skills/odh-component-to-module/references/

    local repo_url
    local ref
    if [[ "${ODH_PLATFORM_TYPE:-OpenDataHub}" == "OpenDataHub" ]]; then
        echo "Downloading manifests for ODH"
        repo_url="https://github.com/opendatahub-io/${repo_name}"
        ref="$odh_ref"
    else
        echo "Downloading manifests for RHDS"
        repo_url="https://github.com/red-hat-data-services/${repo_name}"
        ref="$rhds_ref"
    fi
    # Refs are <branch>@<sha>; the branch is only used by update-manifest-shas.sh.
    local commit_sha="${ref##*@}"

    if [[ -z "${commit_sha}" ]]; then
        echo "[ERROR] No commit SHA for ${component_name} (platform: ${ODH_PLATFORM_TYPE:-OpenDataHub})" >&2
        return 1
    fi

    if [[ "${USE_LOCAL:-}" == "true" ]] && [[ -d "${PROJECT_ROOT}/../${repo_name}" ]]; then
        echo "Copying manifests from adjacent ${repo_name} checkout"
        copy_component_manifests "${component_name}" "${PROJECT_ROOT}/../${repo_name}/${src_path}" "${dst_manifests_dir}"
        echo "Manifests copied to ${dst_manifests_dir}"
        return
    fi

    local tmp_dir=$(mktemp -d -t "odh-${component_name}-manifests.XXXXXXXXXX")

    git -C "${tmp_dir}" init -q
    git -C "${tmp_dir}" remote add origin "${repo_url}"
    git -C "${tmp_dir}" fetch --depth 1 -q origin "${commit_sha}"
    git -C "${tmp_dir}" reset -q --hard "${commit_sha}"

    copy_component_manifests "${component_name}" "${tmp_dir}/${src_path}" "${dst_manifests_dir}"

    rm -rf "${tmp_dir}"

    echo "[${component_name}] Manifests ready at ${dst_manifests_dir}"
}

# Component manifest sources. To upgrade a component, change its SHA(s) below
# and run: make get-manifests (or run make update-refs-shas first to bump every
# ref to the tip of its branch). To add a component, add a new entry.
#
#   <component_name> = "<repo_name>|<src_path>|<odh_branch>@<sha>|<rhds_branch>@<sha>"
#
# The branch is the one whose Konflux build produces the image the manifests
# are deployed with, so manifests and binaries stay in step:
#   - ODH: `stable` for components whose odh-stable image is built from
#     `stable` (maascontroller, aigatewaycontroller), `main` for components
#     that build odh-stable from `main` (batchgateway).
#   - aigrid is excluded from automated bumps (its pins are deliberate
#     release candidates); see SKIP_BUMP in update-manifest-shas.sh.
#   - RHDS: the current RHOAI release branch (rhoai-3.x); RHDS `main` builds
#     no images. Bump the branch name here when a new release branch is cut.
#
# NOTE: RHDS refs are recorded but not used for packaging yet. Nothing sets
# ODH_PLATFORM_TYPE, so config/manifests/ is always fetched from the ODH refs
# and those trees flow unchanged to the RHDS rhoai-3.x branch and the RHOAI
# image (RHOAI images are substituted by opendatahub-operator). A downstream-only
# manifest change on rhoai-3.x therefore does not reach RHOAI until the RHDS
# build fetches or vendors the RHDS trees.
#
# ODH commits:   https://github.com/opendatahub-io/<repo_name>/commits/<branch>
# RHDS commits: https://github.com/red-hat-data-services/<repo_name>/commits/<branch>

declare -A COMPONENTS=(
    [batchgateway]="llm-d-batch-gateway-operator|config|main@93f1c2c021a7ebae6f44c9e1dd05d7c9a34a46e8|rhoai-3.6@dccb5184ef345018d45474ef3ed492179a0ead55"
    [maascontroller]="models-as-a-service|deployment/base/maas-controller|stable@761884ed5a5d8939ca23a84d144821e9615d7115|rhoai-3.6@05d7c7920087b646d394a907967dac9856a4fb57"
    [aigatewaycontroller]="ai-gateway-controller|config/self|stable@ecda771a9a231002b13176058a81ad820eb2c2b4|rhoai-3.6@fd94ad52cbc730aedfb531d9344555185806d05e"
    [aigrid]="praxis-ai-grid-operator|deploy|main@02d6bc4ebcde8673be3ff80e099dbc21a5b7607a|rhoai-3.6@05c8ac3e2e68b82faa91e1696e01a2e293c65400"
)

for component_name in "${!COMPONENTS[@]}"; do
    IFS='|' read -r repo_name src_path odh_ref rhds_ref <<< "${COMPONENTS[$component_name]}"
    fetch_component "$component_name" "$repo_name" "$src_path" "$odh_ref" "$rhds_ref"
done
