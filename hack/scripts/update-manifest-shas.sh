#!/usr/bin/env bash
# Bump bare commit SHA pins in get-manifests.sh to the tip of main on each
# upstream repo. Writes GitHub Actions outputs: updates-needed, fetch-failed,
# failed-components.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS_SCRIPT="${SCRIPT_DIR}/get-manifests.sh"
TRACKED_BRANCH="main"

write_output() {
    local key="$1" value="$2"
    if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
        echo "${key}=${value}" >> "${GITHUB_OUTPUT}"
    fi
}

# Resolve tip SHA for refs/heads/<branch> via git protocol (no API / rate limit).
latest_sha() {
    local org="$1" repo="$2" branch="$3"
    local line sha
    if ! line="$(git ls-remote "https://github.com/${org}/${repo}.git" "refs/heads/${branch}" 2>/dev/null)"; then
        echo "[WARN] Failed to ls-remote ${org}/${repo}@${branch}" >&2
        return 1
    fi
    sha="$(awk '{print $1; exit}' <<< "${line}")"
    if [[ -z "${sha}" || ! "${sha}" =~ ^[0-9a-f]{40}$ ]]; then
        echo "[WARN] No SHA for ${org}/${repo}@${branch}" >&2
        return 1
    fi
    echo "${sha}"
}

# Prints the updated bare SHA on stdout. Exit 1 on fetch failure.
bump_sha() {
    local current_sha="$1" org="$2" repo="$3"
    if [[ -z "${current_sha}" ]]; then
        echo ""
        return 0
    fi
    local new_sha
    if ! new_sha="$(latest_sha "${org}" "${repo}" "${TRACKED_BRANCH}")"; then
        return 1
    fi
    if [[ "${new_sha}" != "${current_sha}" ]]; then
        echo "  ${org}: ${current_sha:0:8} -> ${new_sha:0:8}" >&2
    else
        echo "  ${org}: up to date (${current_sha:0:8})" >&2
    fi
    echo "${new_sha}"
}

# Rewrite one COMPONENTS entry in-place on the temp copy (line-scoped).
# Delimiter is # — values contain | and / but not #.
# Overwrite via cat (not mv) so the destination mode/owner stay intact.
rewrite_component() {
    local file="$1" name="$2" new_value="$3"
    local tmp
    tmp="$(mktemp)"
    if ! sed -E "s#^([[:space:]]*\\[${name}\\]=\")[^\"]*(\".*)\$#\\1${new_value}\\2#" "${file}" > "${tmp}"; then
        rm -f "${tmp}"
        return 1
    fi
    if ! grep -qF "[${name}]=\"${new_value}\"" "${tmp}"; then
        echo "[ERROR] Failed to rewrite COMPONENTS entry for ${name}" >&2
        rm -f "${tmp}"
        return 1
    fi
    cat "${tmp}" > "${file}"
    rm -f "${tmp}"
}

component_value() {
    local repo="$1" src_path="$2" odh_sha="$3" rhds_sha="$4"
    if [[ -n "${rhds_sha}" ]]; then
        echo "${repo}|${src_path}|${odh_sha}|${rhds_sha}"
    else
        echo "${repo}|${src_path}|${odh_sha}"
    fi
}

if [[ ! -f "${MANIFESTS_SCRIPT}" ]]; then
    echo "[ERROR] Missing ${MANIFESTS_SCRIPT}" >&2
    exit 1
fi

mapfile -t ENTRIES < <(sed -n 's/^[[:space:]]*\[\([^]]*\)\]="\([^"]*\)".*/\1|\2/p' "${MANIFESTS_SCRIPT}")

if [[ ${#ENTRIES[@]} -eq 0 ]]; then
    echo "[ERROR] No COMPONENTS entries found in ${MANIFESTS_SCRIPT}" >&2
    exit 1
fi

failed=()
changed=0
tmp_script="$(mktemp)"
# Preserve mode: mktemp is 0600; plain cp keeps that and mv would drop +x on get-manifests.sh.
cp -p "${MANIFESTS_SCRIPT}" "${tmp_script}"

for entry in "${ENTRIES[@]}"; do
    IFS='|' read -r name repo src_path odh_sha rhds_sha <<< "${entry}"
    echo "Checking ${name} (${repo}) on ${TRACKED_BRANCH}"

    new_odh="${odh_sha}"
    new_rhds="${rhds_sha}"

    if [[ -n "${odh_sha}" ]]; then
        if ! new_odh="$(bump_sha "${odh_sha}" "opendatahub-io" "${repo}")"; then
            failed+=("${name}/odh")
            new_odh="${odh_sha}"
        fi
    fi
    if [[ -n "${rhds_sha}" ]]; then
        if ! new_rhds="$(bump_sha "${rhds_sha}" "red-hat-data-services" "${repo}")"; then
            failed+=("${name}/rhds")
            new_rhds="${rhds_sha}"
        fi
    fi

    old_value="$(component_value "${repo}" "${src_path}" "${odh_sha}" "${rhds_sha}")"
    new_value="$(component_value "${repo}" "${src_path}" "${new_odh}" "${new_rhds}")"
    if [[ "${old_value}" != "${new_value}" ]]; then
        changed=$((changed + 1))
        rewrite_component "${tmp_script}" "${name}" "${new_value}"
    fi
done

if [[ "${changed}" -gt 0 ]]; then
    mv "${tmp_script}" "${MANIFESTS_SCRIPT}"
    echo "Updated ${changed} component(s) in ${MANIFESTS_SCRIPT}"
    write_output "updates-needed" "true"
else
    rm -f "${tmp_script}"
    echo "All manifest references are up to date"
    write_output "updates-needed" "false"
fi

if [[ ${#failed[@]} -gt 0 ]]; then
    joined="$(IFS=,; echo "${failed[*]}")"
    write_output "fetch-failed" "true"
    write_output "failed-components" "${joined}"
    echo "[ERROR] Failed to fetch: ${joined}" >&2
    exit 0
fi

write_output "fetch-failed" "false"
