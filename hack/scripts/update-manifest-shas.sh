#!/usr/bin/env bash
# Bump <branch>@<sha> refs in get-manifests.sh to the tip of each ref's branch:
# ODH refs against opendatahub-io/<repo>, RHDS refs against
# red-hat-data-services/<repo>. The branch is kept as-is; change it by hand in
# get-manifests.sh (e.g. when a new rhoai-3.x branch is cut).
#
# RHDS refs are bumped so they stay accurate, but they are not used for
# packaging yet: get-manifests.sh only fetches them when ODH_PLATFORM_TYPE is
# set, and no build sets it. See the NOTE above COMPONENTS in get-manifests.sh.
#
# In GitHub Actions, writes outputs updates-needed, fetch-failed and
# failed-components and exits 0 on fetch failures so the workflow can still
# open a PR for the refs that did resolve. Run locally, it exits 1 on any fetch
# failure so a partial bump is not mistaken for a complete one.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS_SCRIPT="${SCRIPT_DIR}/get-manifests.sh"

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
    if ! line="$(GIT_TERMINAL_PROMPT=0 git -c http.lowSpeedLimit=1 -c http.lowSpeedTime=30 ls-remote "https://github.com/${org}/${repo}.git" "refs/heads/${branch}")"; then
        echo "[WARN] Failed to ls-remote ${org}/${repo}@${branch}" >&2
        return 1
    fi
    sha="$(awk -v ref="refs/heads/${branch}" '$2 == ref {print $1; exit}' <<< "${line}")"
    if [[ -z "${sha}" || ! "${sha}" =~ ^[0-9a-f]{40}$ ]]; then
        echo "[WARN] No SHA for ${org}/${repo}@${branch}" >&2
        return 1
    fi
    echo "${sha}"
}

# Prints the bumped <branch>@<sha> ref on stdout. Exit 1 on a malformed ref or
# fetch failure.
bump_ref() {
    local current_ref="$1" org="$2" repo="$3"
    local branch="${current_ref%@*}" current_sha="${current_ref##*@}"
    if [[ "${current_ref}" != *@* || -z "${branch}" || ! "${current_sha}" =~ ^[0-9a-f]{40}$ ]]; then
        echo "[WARN] ${org}/${repo}: ref '${current_ref}' is not <branch>@<sha>" >&2
        return 1
    fi
    local new_sha
    if ! new_sha="$(latest_sha "${org}" "${repo}" "${branch}")"; then
        return 1
    fi
    if [[ "${new_sha}" != "${current_sha}" ]]; then
        echo "  ${org}@${branch}: ${current_sha:0:8} -> ${new_sha:0:8}" >&2
    else
        echo "  ${org}@${branch}: up to date (${current_sha:0:8})" >&2
    fi
    echo "${branch}@${new_sha}"
}

# Rewrite one COMPONENTS entry in-place on the temp copy (line-scoped).
# Delimiter is # — values contain |, / and @ but not #.
# Overwrite via cat (not mv) so the destination mode/owner stay intact.
rewrite_component() {
    local file="$1" name="$2" new_value="$3"
    local tmp
    tmp="$(mktemp)"
    # Escape sed-special chars in the replacement (& and the # delimiter) so a
    # branch name like release&fix cannot corrupt or abort the rewrite.
    local escaped="${new_value//&/\\&}"
    escaped="${escaped//#/\\#}"
    if ! sed -E "s#^([[:space:]]*\\[${name}\\]=\")[^\"]*(\".*)\$#\\1${escaped}\\2#" "${file}" > "${tmp}"; then
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
    local repo="$1" src_path="$2" odh_ref="$3" rhds_ref="$4"
    if [[ -n "${rhds_ref}" ]]; then
        echo "${repo}|${src_path}|${odh_ref}|${rhds_ref}"
    else
        echo "${repo}|${src_path}|${odh_ref}"
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

# Components excluded from automated bumps: their refs stay <branch>@<sha> in
# get-manifests.sh (accurate provenance) and are advanced by hand — e.g.
# aigrid pins deliberate release candidates rather than the tip of main.
declare -A SKIP_BUMP=(
    [aigrid]=1
)

failed=()
changed=0
tmp_script="$(mktemp)"
# Preserve mode: mktemp is 0600; plain cp keeps that and mv would drop +x on get-manifests.sh.
cp -p "${MANIFESTS_SCRIPT}" "${tmp_script}"

for entry in "${ENTRIES[@]}"; do
    IFS='|' read -r name repo src_path odh_ref rhds_ref <<< "${entry}"
    if [[ -n "${SKIP_BUMP[${name}]:-}" ]]; then
        echo "Checking ${name} (${repo}) — excluded from automated bumps (SKIP_BUMP), leaving as-is"
        continue
    fi
    echo "Checking ${name} (${repo})"

    new_odh="${odh_ref}"
    new_rhds="${rhds_ref}"

    if [[ -n "${odh_ref}" ]]; then
        if ! new_odh="$(bump_ref "${odh_ref}" "opendatahub-io" "${repo}")"; then
            failed+=("${name}/odh")
            new_odh="${odh_ref}"
        fi
    fi
    if [[ -n "${rhds_ref}" ]]; then
        if ! new_rhds="$(bump_ref "${rhds_ref}" "red-hat-data-services" "${repo}")"; then
            failed+=("${name}/rhds")
            new_rhds="${rhds_ref}"
        fi
    fi

    old_value="$(component_value "${repo}" "${src_path}" "${odh_ref}" "${rhds_ref}")"
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
    if [[ -z "${GITHUB_OUTPUT:-}" ]]; then
        exit 1
    fi
    exit 0
fi

write_output "fetch-failed" "false"
