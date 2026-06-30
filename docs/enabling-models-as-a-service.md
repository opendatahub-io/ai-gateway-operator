# Enabling Models as a Service (MaaS)

## Overview

Models as a Service (MaaS) provides multi-tenant AI/ML model management with API key authentication, model subscriptions, and gateway routing.

**Deployment scope:** ai-gateway-operator deploys the **MaaS controller layer only** — CRDs, RBAC, the `maas-controller` Deployment, and its validating webhook (`config/manifests/maascontroller/base/`). It does **not** deploy maas-api, billing, observability dashboards, or gateway policies from `config/manifests/maas/`; those remain the platform operator's responsibility.

## Prerequisites

- OpenShift AI or Open Data Hub installed
- Gateway API CRDs installed
- ai-gateway-operator deployed
- Cluster admin permissions

## Quick Start

### Enable MaaS

Create or update the AIGateway CR:

```yaml
apiVersion: components.platform.opendatahub.io/v1alpha1
kind: AIGateway
metadata:
  name: default-aigateway
spec:
  modelsAsService:
    managementState: Managed  # Use "Removed" to disable
```

Apply:
```bash
kubectl apply -f aigateway.yaml
```

### Verify Deployment

Check status:
```bash
# Verify AIGateway is ready
kubectl get aigateway default-aigateway

# Verify maas-controller deployment
kubectl get deployment -n opendatahub maas-controller

# Verify MaaS CRDs (should list 7)
kubectl get crd | grep maas
```

### Verify Default Tenant

```bash
kubectl get tenant -n models-as-a-service default-tenant
```

**Note:** Tenant shows `GatewayNotReady` until Gateway CR is deployed by the platform operator.

## Troubleshooting

### maas-controller CrashLoopBackOff

**Expected behavior** when KServe/Kuadrant operators are not installed. Controller remains functional between restarts.

**Solutions:**
- Install KServe operator (recommended for LLM support)
- Install Kuadrant operator (recommended for rate limiting)
- Accept restart loop (controller works between restarts)

### Tenant Stuck in GatewayNotReady

Ensure Gateway CR exists:
```bash
kubectl get gateway -n openshift-ingress maas-default-gateway
```

Contact platform administrator if missing.

### View Logs

```bash
# ai-gateway-operator logs
kubectl logs -n ai-gateway-system deployment/ai-gateway-operator

# maas-controller logs
kubectl logs -n ai-gateway-system deployment/maas-controller
```

## Additional Resources

- [MaaS Controller Docs](https://github.com/opendatahub-io/models-as-a-service)
- [Architecture](architecture.md)
