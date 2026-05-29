# Architecture: opendatahub-operator + opendatahub-ai-gateway-operator

This document describes how the ODH platform operator and the AI Gateway module operator work together to deploy the batch-gateway stack.

## 1. Overview

The system is a three-layer operator hierarchy:

```
User
 │
 │  creates DataScienceCluster CR
 ▼
┌──────────────────────────────────────────────────┐
│  opendatahub-operator  (platform operator)       │
│                                                  │
│  Modules controller watches DSC, for each        │
│  enabled module:                                 │
│   1. Renders the module's Helm chart             │
│   2. Deploys module operator + CRD               │
│   3. Creates the module CR (e.g. AIGateway)      │
│   4. Reads module CR status for DSC aggregation  │
└──────────────┬───────────────────────────────────┘
               │  Helm install → Deployment, RBAC, CRD
               │  SSA apply   → AIGateway CR
               ▼
┌──────────────────────────────────────────────────┐
│  opendatahub-ai-gateway-operator  (module operator)          │
│                                                  │
│  Watches AIGateway CR, for each managed          │
│  sub-component:                                  │
│   1. Renders kustomize manifests                 │
│   2. Deploys sub-component via SSA               │
│   3. Reports status back on AIGateway CR         │
└──────────────┬───────────────────────────────────┘
               │  kustomize render + SSA
               ▼
┌──────────────────────────────────────────────────┐
│  batch-gateway-operator  (sub-component)         │
│                                                  │
│  Watches LLMBatchGateway CR, manages actual      │
│  batch inference gateway workloads               │
└──────────────────────────────────────────────────┘
```

## 2. Build process

### 2.1 Each sub-component prepares its manifests

Each sub-component operator (e.g. batch-gateway-operator) lives in its own upstream repo and provides a standard kustomize layout under its `config/` directory, including:
- **CRD** (`crd/bases/`) — the custom resource the sub-component operator watches (e.g. `LLMBatchGateway`).
- **Manager** (`manager/`) — the Deployment for the sub-component operator.
- **RBAC** (`rbac/`) — ClusterRole, ClusterRoleBinding, ServiceAccount, leader election role.
- **Overlays** (`overlays/odh/`, `overlays/rhoai/`) — platform-specific kustomize overlays for ODH and RHOAI.

### 2.2 opendatahub-ai-gateway-operator fetches sub-component manifests

`make get-manifests` (`hack/scripts/get-manifests.sh`) fetches each sub-component's manifests from its upstream repo at a pinned commit SHA and copies them into `config/manifests/<component>/` (e.g. `config/manifests/batchgateway/`).
- The fetched files must be committed to git so that PR review can catch manifest changes and container builds remain reproducible without network access.
- At build time, `Containerfile` copies these manifests into the container image at `/manifests/` for the controller to use at runtime.
- To upgrade a sub-component, update the SHA in `get-manifests.sh`, re-run `make get-manifests`, and commit the result.

### 2.3 opendatahub-ai-gateway-operator generates RBAC and Helm chart

`make manifests` generates `config/rbac/role.yaml` from kubebuilder RBAC markers in `aigateway_controller.go`. These markers must include permissions for all sub-component workloads (RBAC escalation).

`make helm` (`cmd/chartgen`) reads the kustomize overlay (`config/default/`) and picks up `config/rbac/role.yaml`, CRDs, Deployment, ConfigMap, etc. The output is written to `config/chart/` and must be committed to git. opendatahub-operator's build process fetches this chart from our repo at a pinned commit (see 2.4).

### 2.4 opendatahub-operator consumes the Helm chart

During opendatahub-operator's build, `get_all_manifests.sh` downloads each module's Helm chart from its repo at a pinned commit SHA (configured in `ODH_COMPONENT_CHARTS` / `RHOAI_COMPONENT_CHARTS` maps). The downloaded charts are bundled into the opendatahub-operator container image at `/opt/charts/`. 

At runtime, the modules controller reads charts from this path (`DEFAULT_CHARTS_PATH=/opt/charts`) to render and deploy module operators via SSA.

## 3. Reconciliation flow

The following walkthrough uses batch-gateway as an example sub-component to illustrate the end-to-end reconciliation flow.

### 3.1 User creates a DataScienceCluster CR
1. User creates a `DataScienceCluster` (DSC) CR with **aigateway** set to `Managed`.

```yaml
apiVersion: datasciencecluster.opendatahub.io/v2
kind: DataScienceCluster
metadata:
  name: default-dsc
spec:
  components:
    aigateway:
      managementState: Managed
```

### 3.2 opendatahub-operator → opendatahub-ai-gateway-operator
2. opendatahub-operator watches the `DataScienceCluster` CR and sees `aigateway` set to `Managed`.
3. opendatahub-operator renders the opendatahub-ai-gateway-operator Helm chart (bundled at `/opt/charts/` in its container image) and deploys it via SSA:

```bash
$ oc get deployment -n opendatahub -l app.kubernetes.io/name=opendatahub-ai-gateway-operator
NAME                               READY   UP-TO-DATE   AVAILABLE
opendatahub-ai-gateway-operator    1/1     1            1
```

4. opendatahub-operator creates the `AIGateway` CR with `batchGateway` set to `Managed`:

```yaml
apiVersion: components.platform.opendatahub.io/v1alpha1
kind: AIGateway
metadata:
  name: default-aigateway
spec:
  batchGateway:
    managementState: Managed
```

### 3.3 opendatahub-ai-gateway-operator → sub-component operators
5. opendatahub-ai-gateway-operator's controller watches the `AIGateway` CR.
6. opendatahub-ai-gateway-operator reads the spec (e.g. `batchGateway.managementState: Managed`), renders `config/manifests/batchgateway/` via kustomize, and deploys the resources via SSA:

```bash
$ oc get deployment -n opendatahub -l app.kubernetes.io/name=batch-gateway-operator
NAME                                        READY   UP-TO-DATE   AVAILABLE
batch-gateway-operator-controller-manager   1/1     1            1
```

7. opendatahub-ai-gateway-operator updates `AIGateway` CR status with required conditions (`Ready`, `ProvisioningSucceeded`, `Degraded`) and `observedGeneration`. opendatahub-operator reads this status to aggregate into the DSC.
8. batch-gateway-operator starts running and watches `LLMBatchGateway` CRD.

### 3.4 sub-component operators → workload
9. Users create the `LLMBatchGateway` CR to provision actual workloads.
10. batch-gateway-operator watches `LLMBatchGateway` CR and deploys batch-gateway workloads.


## 4. References

- [FeatureRefinement - RHAISTRAT-1064 - Implement Modular Architecture for ODH Operator](https://docs.google.com/document/d/1qGvaUsioOXl1MPm0TqSxaYR6booRyDLxz_-wTYVF8hM/edit?tab=t.3mrf1syv46a)
- [Onboarding Guide for ODH Operator Modules](https://docs.google.com/document/d/1FgN_U-6XH8M-Mu6XNeldUlTPsnw7UyPCWg5NVJJdYnw/edit?usp=sharing)
- [Module Handler Developer Guide](https://gitlab.cee.redhat.com/data-hub/odh-modularisation-docs/-/blob/main/Module%20Handler%20Developer%20Guide.md?ref_type=heads)
- [opendatahub-module-operator](https://github.com/lburgazzoli/opendatahub-module-operator)
- [odh-platform-utilities](https://github.com/opendatahub-io/odh-platform-utilities)