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

# The maas-discovery service is not shippable in 3.6 (feature not ready; picked
# up again next release). Upstream models-as-a-service keeps the
# MAAS_DISCOVERY_* knobs in deployment/base/maas-controller for ODH-side
# development, so every vendoring sync must strip them here to keep the service
# un-enableable on product installs: with the env vars absent from the
# maas-controller Deployment, editing the maas-parameters ConfigMap has no
# effect, and the controller's DiscoveryEnabled defaults to false (its
# reconcile loop tears down any leftover discovery resources on upgrade).
# The maas-discovery-image reference is deliberately KEPT so the
# CSV-declared RELATED_IMAGE_ODH_MAAS_DISCOVERY_IMAGE still resolves.
# Delete this special case when the feature ships and the upstream manifests
# are safe to vendor as-is again.
strip_maas_discovery_knobs() {
    local dst="$1"

    # Remove the MAAS_DISCOVERY_{ENABLED,REPLICAS,LOG_LEVEL} env blocks from the
    # manager Deployment. Skip from the "- name:" line to the first non-blank
    # line indented no deeper than that item (the next env entry or a container
    # field), so an unexpected upstream block shape ends the skip at the entry
    # boundary instead of running through the next "optional: true" line and
    # eating an unrelated env block.
    awk '
        /^ *- name: MAAS_DISCOVERY_(ENABLED|REPLICAS|LOG_LEVEL)$/ {
            match($0, /^ */); dash = RLENGTH; skip = 1; next
        }
        skip {
            match($0, /^ */)
            if (RLENGTH > dash || $0 ~ /^ *$/) next
            skip = 0
        }
        { print }
    ' "${dst}/manager/manager.yaml" > "${dst}/manager/manager.yaml.tmp" &&
    mv "${dst}/manager/manager.yaml.tmp" "${dst}/manager/manager.yaml"

    # Drop the corresponding maas-parameters keys from every params.env
    # (default and overlays).
    find "${dst}" -name params.env -exec sed -i \
        -e '/^maas-discovery-enabled=/d' \
        -e '/^maas-discovery-replicas=/d' \
        -e '/^maas-discovery-log-level=/d' \
        {} +

    # Fail loudly if anything survived: the upstream manifest shape has likely
    # changed and this strip needs updating.
    if grep -rn -E 'name: MAAS_DISCOVERY_(ENABLED|REPLICAS|LOG_LEVEL)$|^maas-discovery-(enabled|replicas|log-level)=' "${dst}"; then
        echo "[ERROR] maascontroller: failed to strip maas-discovery knobs;" >&2
        echo "        update strip_maas_discovery_knobs in get-manifests.sh" >&2
        return 1
    fi
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
        if [[ "${component_name}" == "maascontroller" ]]; then
            strip_maas_discovery_knobs "${dst}"
        fi
    fi
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
# and run: make get-manifests. To add a component, add a new entry.
#
#   <component_name> = "<repo_name>|<src_path>|<odh_commit>|<rhds_commit>"
#
# ODH commits:   https://github.com/opendatahub-io/<repo_name>/commits/
# RHDS commits: https://github.com/red-hat-data-services/<repo_name>/commits/

declare -A COMPONENTS=(
    [batchgateway]="llm-d-batch-gateway-operator|config|93f1c2c021a7ebae6f44c9e1dd05d7c9a34a46e8|93f1c2c021a7ebae6f44c9e1dd05d7c9a34a46e8"
    [maascontroller]="models-as-a-service|deployment/base/maas-controller|2436307c83c76b493c56832ec1fa4aee0bb6f2b5|2436307c83c76b493c56832ec1fa4aee0bb6f2b5"
    [aigatewaycontroller]="ai-gateway-controller|config/self|ecda771a9a231002b13176058a81ad820eb2c2b4|ecda771a9a231002b13176058a81ad820eb2c2b4"
    [aigrid]="praxis-ai-grid-operator|deploy|db11454660b7be62bccffe4888560c1636d39305|c863e173049a921160843237b6b03818eaa12b1b"
)

for component_name in "${!COMPONENTS[@]}"; do
    IFS='|' read -r repo_name src_path odh_commit rhds_commit <<< "${COMPONENTS[$component_name]}"
    fetch_component "$component_name" "$repo_name" "$src_path" "$odh_commit" "$rhds_commit"
done
