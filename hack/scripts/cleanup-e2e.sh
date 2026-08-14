#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

NAMESPACE="${1:-ai-gateway-system}"
CR_RESOURCE="aigateways.components.platform.opendatahub.io"

echo "Cleaning up e2e test resources..."

# Delete component CRs first and wait for them to disappear before removing the operator or CRD.
kubectl delete "${CR_RESOURCE}" --all --ignore-not-found 2>/dev/null || true
kubectl wait --for=delete "${CR_RESOURCE}" --all --timeout=60s 2>/dev/null || true

# Remove the operator (Deployment, RBAC, CRD, etc.) rendered from config/default.
kubectl delete -k "${PROJECT_ROOT}/config/default" --ignore-not-found --wait 2>/dev/null || true

# Delete namespace
kubectl delete namespace "${NAMESPACE}" --ignore-not-found 2>/dev/null || true

# Delete any leftover cluster-scoped resources
kubectl delete clusterroles -l platform.opendatahub.io/part-of=aigateway --ignore-not-found 2>/dev/null || true
kubectl delete clusterrolebindings -l platform.opendatahub.io/part-of=aigateway --ignore-not-found 2>/dev/null || true

# Delete CRD if still present (the kustomize delete above should have removed it)
kubectl delete crd "${CR_RESOURCE}" --ignore-not-found 2>/dev/null || true

echo "E2E test cleanup complete."
