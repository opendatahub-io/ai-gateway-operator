#!/usr/bin/env bash
set -euo pipefail

# Verifies that every flag in a vendored manager manifest is accepted by the
# image that will actually run alongside it.
#
# Why this exists (RHAI-15815): a component's *args* are vendored from a pinned
# upstream commit, while its *image* is built from a separate branch on its own
# schedule. Nothing compared the two, so a manifest that gained
# --metrics-secure=true reached clusters alongside an image predating that flag.
# The operand parses its command line with an ExitOnError flag set, exited 2 on
# the unknown flag, crashlooped, and took the MaaS readiness chain down with it.
#
# A branch-ancestry or revision-label check cannot catch this: branch topology is
# a present-tense fact and Konflux stamps the commit only as a PipelineRun
# annotation, not an image label. Asking the image itself — via --help — measures
# the interface that actually has to agree.
#
# Usage:
#   verify-manifest-flags.sh [--image <component>=<ref>]... [--skip-missing-image] [<component>]...
#
#   --image <component>=<ref>  Override the image for one component instead of
#                              reading it from the component's params.env.
#   --skip-missing-image       Treat a pull/run failure as a warning rather than
#                              a failure (for runs without registry access).
#
# CONTAINER_PLATFORM overrides the platform passed to run, e.g. linux/amd64 when
# working on an arm64 host against an amd64-only image.
#
# With no component arguments, every component with a declared check is
# verified. Vendoring a component without a check here is reported, not silently
# passed.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CONTAINER_TOOL="${CONTAINER_TOOL:-podman}"

log() {
    printf '%s\n' "$*" >&2
}

usage() {
    sed -n '/^# Usage:/,/^# passed\./p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
}

# Declared checks: <component>|<vendored manifest>|<binary in image>|<params.env>|<image key>
#
# Only components whose Deployment args come from an independently-built image
# need an entry. CRD/overlay-only components have no CLI contract to get wrong.
CHECKS=(
    "aigatewaycontroller|config/manifests/aigatewaycontroller/manager/manager.yaml|/manager|config/manifests/aigatewaycontroller/default/params.env|ai-gateway-controller-image"
)

# Empty-array expansions below use ${arr[@]+"${arr[@]}"}: bash 3.2 (macOS)
# reports an empty array as unbound under set -u.

# IMAGE_OVERRIDES holds "<component>=<ref>" entries.
IMAGE_OVERRIDES=()
SKIP_MISSING_IMAGE=false
WANTED=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --image)
            [[ $# -ge 2 ]] || { log "--image requires <component>=<ref>"; exit 1; }
            [[ "$2" == *=* ]] || { log "--image requires <component>=<ref>, got: $2"; exit 1; }
            IMAGE_OVERRIDES+=("$2")
            shift 2
            ;;
        --skip-missing-image)
            SKIP_MISSING_IMAGE=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        -*)
            log "unknown option: $1"
            usage
            exit 1
            ;;
        *)
            WANTED+=("$1")
            shift
            ;;
    esac
done

if ! command -v "${CONTAINER_TOOL}" >/dev/null 2>&1; then
    log "${CONTAINER_TOOL} is required (override with CONTAINER_TOOL)"
    exit 1
fi

override_image() {
    local component="$1" entry
    for entry in ${IMAGE_OVERRIDES[@]+"${IMAGE_OVERRIDES[@]}"}; do
        if [[ "${entry%%=*}" == "${component}" ]]; then
            printf '%s' "${entry#*=}"
            return 0
        fi
    done
    return 0
}

# manifest_flag_names prints the flag names (without leading dashes) of every
# entry in the first `args:` list of a container spec.
#
# YAML may quote an entry (`- '--new-flag=true'`); the quotes are syntax, not
# part of the value the container receives, so they are stripped before the
# entry is inspected. Skipping quoted entries instead would report success for
# a flag the image may reject.
manifest_flag_names() {
    local file="$1"
    awk '
        /^[[:space:]]*args:[[:space:]]*$/ { inargs = 1; next }
        inargs && /^[[:space:]]*$/ { next }
        inargs && /^[[:space:]]*-[[:space:]]/ {
            line = $0
            sub(/^[[:space:]]*-[[:space:]]*/, "", line)
            gsub(/^["\047]|["\047]$/, "", line)
            if (line ~ /^-/) {
                name = line
                sub(/^-+/, "", name)
                sub(/=.*$/, "", name)
                print name
            }
            next
        }
        # The first list is the one under test; a later container would have its
        # args validated against an image that never runs them.
        inargs { exit }
    ' "${file}"
}

params_value() {
    local file="$1" key="$2"
    [[ -f "${file}" ]] || return 0
    awk -F= -v k="${key}" '$1 == k { sub(/^[^=]*=/, ""); print; exit }' "${file}"
}

# run_help runs the operand with --help and returns its combined output.
run_help() {
    local image="$1" binary="$2"
    local -a platform_arg=()
    if [[ -n "${CONTAINER_PLATFORM:-}" ]]; then
        platform_arg=(--platform "${CONTAINER_PLATFORM}")
    fi
    "${CONTAINER_TOOL}" run --rm ${platform_arg[@]+"${platform_arg[@]}"} \
        --entrypoint "${binary}" "${image}" --help 2>&1
}

# looks_like_usage reports whether text contains at least one flag definition,
# the shape the flag package prints ("  -metrics-secure"). Without this a failed
# run — no matching platform, unreachable registry, missing binary — would be
# read as usage text and make every flag look unsupported, reporting the wrong
# failure.
looks_like_usage() {
    grep -qE "^[[:space:]]+-[A-Za-z][A-Za-z0-9-]*" <<< "$1"
}

# image_accepts_flag reports whether the flag is defined in the image's usage
# text. The match is anchored to the start of a usage line, which is the only
# place the flag package defines a flag ("  -metrics-secure"). Matching anywhere
# would accept a name mentioned only in another flag's description, so a removed
# flag could still be reported ok while the image rejects it at startup.
image_accepts_flag() {
    local help_text="$1" name="$2"
    grep -qE "^[[:space:]]+-{1,2}${name}([[:space:]=,]|\$)" <<< "${help_text}"
}

status=0
checked=0
skipped=0

for check in "${CHECKS[@]}"; do
    IFS='|' read -r component manifest binary params_env image_key <<< "${check}"

    if [[ ${#WANTED[@]} -gt 0 ]]; then
        match=false
        for want in ${WANTED[@]+"${WANTED[@]}"}; do
            [[ "${want}" == "${component}" ]] && match=true
        done
        [[ "${match}" == true ]] || continue
    fi

    manifest_path="${PROJECT_ROOT}/${manifest}"
    if [[ ! -f "${manifest_path}" ]]; then
        log "FAIL ${component}: manifest not found at ${manifest}; run make get-manifests"
        status=1
        continue
    fi

    image="$(override_image "${component}")"
    if [[ -z "${image}" ]]; then
        image="$(params_value "${PROJECT_ROOT}/${params_env}" "${image_key}")"
    fi
    if [[ -z "${image}" ]]; then
        log "FAIL ${component}: no image reference (set --image ${component}=<ref> or populate ${params_env}:${image_key})"
        status=1
        continue
    fi

    flags=()
    while IFS= read -r name; do
        [[ -n "${name}" ]] && flags+=("${name}")
    done < <(manifest_flag_names "${manifest_path}")

    if [[ ${#flags[@]} -eq 0 ]]; then
        log "FAIL ${component}: no args found in ${manifest}"
        status=1
        continue
    fi

    log "checking ${component}: ${#flags[@]} flag(s) against ${image}"

    # --help conventionally exits non-zero, so the exit code says nothing; judge
    # the output instead.
    help_text="$(run_help "${image}" "${binary}")" || true
    if ! looks_like_usage "${help_text}"; then
        if [[ "${SKIP_MISSING_IMAGE}" == true ]]; then
            log "WARN ${component}: could not read usage from ${image}; skipping"
            skipped=$((skipped + 1))
            continue
        fi
        log "FAIL ${component}: could not read usage from ${image}"
        log "       first line of output: $(printf '%s' "${help_text}" | head -1)"
        log "       check the image reference, ${binary} in the image, and CONTAINER_PLATFORM"
        status=1
        continue
    fi

    checked=$((checked + 1))
    for name in ${flags[@]+"${flags[@]}"}; do
        if image_accepts_flag "${help_text}" "${name}"; then
            log "  ok   --${name}"
        else
            log "  FAIL --${name} is not accepted by ${image}"
            log "       the pinned manifest is newer than the image: publish an image that"
            log "       defines --${name}, or drop the arg from ${manifest} if it only restates a default"
            status=1
        fi
    done
done

# Make the coverage gap visible rather than implied.
for manifest_dir in "${PROJECT_ROOT}"/config/manifests/*/; do
    component="$(basename "${manifest_dir}")"
    declared=false
    for check in "${CHECKS[@]}"; do
        IFS='|' read -r declared_component _ <<< "${check}"
        [[ "${declared_component}" == "${component}" ]] && declared=true
    done
    [[ "${declared}" == true ]] || log "note: ${component} has no manifest/flag check declared"
done

# A skipped check is not a missing check: --skip-missing-image documents a run
# failure as a warning, so it must not turn into "no declared check matched"
# when every selected component was skipped.
if [[ ${#WANTED[@]} -gt 0 && "${checked}" -eq 0 && "${skipped}" -eq 0 ]]; then
    log "no declared check matched: ${WANTED[*]}"
    exit 1
fi

if [[ "${status}" -ne 0 ]]; then
    log "manifest/flag verification failed"
    exit 1
fi

log "manifest/flag verification passed"
