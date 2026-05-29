#!/usr/bin/env bash
set -euo pipefail

log() {
    printf '%s\n' "$*" >&2
}

usage() {
    cat >&2 <<'EOF'
Usage: resolve-image-ref.sh <image-ref>

Print a canonical image reference for Helm deployment.
If the input already uses a digest, it is returned unchanged.
Otherwise the script tries to resolve the image tag to a digest via the local
container tool, pulling the image if needed. If digest lookup still fails,
the original input reference is returned.
EOF
}

image_ref="${1:-}"
container_tool="${CONTAINER_TOOL:-podman}"

if [[ -z "${image_ref}" ]]; then
    usage
    exit 1
fi

if ! command -v "${container_tool}" >/dev/null 2>&1; then
    log "${container_tool} is required"
    exit 1
fi

if [[ "${image_ref}" == *@sha256:* ]]; then
    printf '%s\n' "${image_ref}"
    exit 0
fi

if [[ "${image_ref}" == *.svc:*/* || "${image_ref}" == *.svc/* ]]; then
    log "leaving in-cluster service image reference unchanged: ${image_ref}"
    printf '%s\n' "${image_ref}"
    exit 0
fi

image_name="${image_ref%%@*}"
last_segment="${image_name##*/}"
repo_name="${image_name}"
if [[ "${last_segment}" == *:* ]]; then
    repo_name="${image_name%:*}"
fi

resolve_repo_digest() {
    local ref="$1"
    local repo="$2"
    local digest_ref=""
    local candidate=""

    while IFS= read -r candidate; do
        [[ -z "${candidate}" ]] && continue
        if [[ "${candidate}" == "${repo}@"* ]]; then
            digest_ref="${candidate}"
            break
        fi
        if [[ -z "${digest_ref}" ]]; then
            digest_ref="${candidate}"
        fi
    done < <("${container_tool}" image inspect "${ref}" --format '{{range .RepoDigests}}{{println .}}{{end}}' 2>/dev/null || true)

    printf '%s\n' "${digest_ref}"
}

digest_ref="$(resolve_repo_digest "${image_ref}" "${repo_name}")"
if [[ -z "${digest_ref}" ]]; then
    log "digest not available locally for ${image_ref}, pulling to resolve"
    "${container_tool}" pull "${image_ref}" >/dev/null
    digest_ref="$(resolve_repo_digest "${image_ref}" "${repo_name}")"
fi

if [[ -n "${digest_ref}" ]]; then
    printf '%s\n' "${digest_ref}"
else
    log "falling back to tag reference for ${image_ref}"
    printf '%s\n' "${image_ref}"
fi
