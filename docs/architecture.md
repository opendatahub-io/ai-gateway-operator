# Architecture: ai-gateway-operator

This document describes how **ai-gateway-operator** (the AI Gateway module operator) manages its sub-components — fetching their manifests, deploying their operators, and aggregating their status onto the `AIGateway` CR.

For how this operator integrates with the ODH platform operator (DataScienceCluster, manifest packaging, status roll-up to the DSC), see [integration-opendatahub-operator.md](integration-opendatahub-operator.md).

## 1. Overview

ai-gateway-operator watches a single `AIGateway` CR and, for each managed sub-component (e.g. batch-gateway, maas), renders and deploys that sub-component's operator via server-side apply (SSA):

```
AIGateway CR
 │
 ▼
┌──────────────────────────────────────────────────┐
│  ai-gateway-operator  (module operator)          │
│                                                  │
│  Watches AIGateway CR, for each managed          │
│  sub-component:                                  │
│   1. Renders kustomize manifests                 │
│   2. Deploys sub-component via SSA               │
│   3. Reports status back on AIGateway CR         │
└──────────────┬───────────────────────────────────┘
               │  kustomize render + SSA (per managed sub-component)
               │
       ┌───────┴────────────────────┐
       ▼                            ▼
┌───────────────────────────┐  ┌───────────────────────────┐
│  batch-gateway-operator   │  │  maas-controller          │
│  (sub-component)          │  │  (sub-component)          │
│                           │  │                           │
│  Watches LLMBatchGateway  │  │  Watches Tenant,          │
│  CR, manages batch        │  │  MaaSSubscription,        │
│  inference gateway        │  │  MaaSModelRef CRs,        │
│  workloads                │  │  manages multi-tenant     │
│                           │  │  model inference          │
└───────────────────────────┘  └───────────────────────────┘
```

The `AIGateway` CR itself is created by opendatahub-operator — see [integration-opendatahub-operator.md](integration-opendatahub-operator.md).

### MaaS deployment scope

When `spec.modelsAsService.managementState` is `Managed`, ai-gateway-operator renders and deploys **`config/manifests/maascontroller/default/`**:

| Included | Not deployed by this operator |
|----------|------------------------------|
| MaaS CRDs | maas-api |
| maas-controller Deployment | Billing |
| Controller RBAC + webhook | Observability (Grafana/Perses) |
| | Gateway policies (`config/manifests/maas/`) |

The operator's RBAC escalation rules in `config/rbac/role.yaml` must cover permissions inside the vendored `maascontroller` ClusterRoles (see kubebuilder markers in `aigateway_controller.go`). Do not edit `config/manifests/maascontroller/rbac/` directly — it is refreshed by `make get-manifests`.

## 2. Build process

### 2.1 Each sub-component prepares its manifests

Each sub-component operator (e.g. batch-gateway-operator) lives in its own midstream repo and provides a standard kustomize layout under its `config/` directory, including:
- **CRD** (`crd/bases/`) — the custom resource the sub-component operator watches (e.g. `LLMBatchGateway`).
- **Manager** (`manager/`) — the Deployment for the sub-component operator.
- **RBAC** (`rbac/`) — ClusterRole, ClusterRoleBinding, ServiceAccount, leader election role.
- **Overlays** (`overlays/odh/`, `overlays/rhoai/`) — platform-specific kustomize overlays for ODH and RHDS.

### 2.2 ai-gateway-operator fetches sub-component manifests

`make get-manifests` (`hack/scripts/get-manifests.sh`) fetches each sub-component's manifests from its repo at a pinned commit SHA and copies them into `config/manifests/<sub-component>/` (e.g. `config/manifests/batchgateway/`).
- The fetched files must be committed to git so that PR review can catch manifest changes and container builds remain reproducible without network access.
- At build time, `Containerfile` copies these manifests into the container image at `/opt/manifests/`; an init container copies them into a writable emptyDir at runtime (see `config/manager/manager.yaml`).
- To upgrade a sub-component, update the SHA in `get-manifests.sh`, re-run `make get-manifests`, and commit the result.

### 2.3 ai-gateway-operator generates its own deploy manifests

`make manifests` generates `config/rbac/role.yaml` from kubebuilder RBAC markers in `aigateway_controller.go`. These markers must include permissions for all sub-component workloads (RBAC escalation).

The operator's own deploy manifests (CRD, RBAC, Deployment, ConfigMap, metrics Service) live as a kustomize tree under `config/`. Two consumers render it:
- **Local/dev:** `make deploy` builds `config/default/` and applies it to the cluster.
- **opendatahub-operator:** consumes the platform overlay `config/manifests/ai-gateway-operator/overlays/{odh,rhoai}`. The operator image is parameterized via `config/manifests/ai-gateway-operator/base/params.env` (`AI_GATEWAY_OPERATOR_IMAGE`), which opendatahub-operator substitutes at deploy time. See [integration-opendatahub-operator.md](integration-opendatahub-operator.md).

Both reuse the same `config/crd`, `config/rbac`, and `config/manager`, so the deploy manifests never drift from `make manifests` output.

## 3. Reconciliation flow

The following walkthrough uses batch-gateway as an example sub-component to illustrate how ai-gateway-operator reconciles the `AIGateway` CR down to running workloads. (For how the `AIGateway` CR is created in the first place, see [integration-opendatahub-operator.md](integration-opendatahub-operator.md).)

### 3.1 ai-gateway-operator → sub-component operators
1. ai-gateway-operator's controller watches the `AIGateway` CR.
2. ai-gateway-operator reads the spec (e.g. `batchGateway.managementState: Managed`), renders `config/manifests/batchgateway/` via kustomize, and deploys the resources via SSA:

```bash
$ oc get deployment -n opendatahub -l app.kubernetes.io/name=batch-gateway-operator
NAME                                        READY   UP-TO-DATE   AVAILABLE
batch-gateway-operator-controller-manager   1/1     1            1
```

3. batch-gateway-operator starts running and watches the `LLMBatchGateway` CRD.
4. ai-gateway-operator sets `DeploymentsAvailable=True` on the `AIGateway` CR only when **every** managed sub-component Deployment reports all replicas ready (e.g. `1/1`) — these are the Deployments it labeled `platform.opendatahub.io/part-of=aigateway` (the value derives from the parent `AIGateway` CR's Kind, so every managed sub-component's Deployment shares it). If any managed sub-component is not ready (e.g. `batch-gateway-operator` is up but `maas` is not), `DeploymentsAvailable` stays `False` and the aggregate `Ready` does **not** flip to true. Once `DeploymentsAvailable=True`, the framework aggregates it into the `Ready` / `ProvisioningSucceeded` / `Degraded` conditions and updates `observedGeneration`. opendatahub-operator reads this status to aggregate into the DSC.

### 3.2 sub-component operators → workload
5. Users create the `LLMBatchGateway` CR to provision actual workloads.
6. batch-gateway-operator watches the `LLMBatchGateway` CR and deploys batch-gateway workloads.

## 4. References

- [Module Handler Developer Guide](https://gitlab.cee.redhat.com/data-hub/odh-modularisation-docs/-/blob/main/Module%20Handler%20Developer%20Guide.md?ref_type=heads)
- [opendatahub-module-operator](https://github.com/lburgazzoli/opendatahub-module-operator)
- [odh-platform-utilities](https://github.com/opendatahub-io/odh-platform-utilities)
