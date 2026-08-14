# Integration: opendatahub-operator + ai-gateway-operator

This document describes how **ai-gateway-operator** plugs into the ODH platform operator as a module: how opendatahub-operator deploys it from kustomize manifests, creates its `AIGateway` CR from the `DataScienceCluster`, and rolls its status back up into the DSC.

For how ai-gateway-operator manages its own sub-components once the `AIGateway` CR exists, see [architecture.md](architecture.md).

## 1. Overview

opendatahub-operator (the platform operator) watches the `DataScienceCluster` (DSC) CR. For each enabled module it renders the module's kustomize manifests, deploys the module operator + CRD, creates the module CR, and reads the module CR status back for DSC aggregation:

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
│   1. Renders the module's kustomize manifests    │
│   2. Deploys module operator + CRD               │
│   3. Creates the module CR (e.g. AIGateway)      │
│   4. Reads module CR status for DSC aggregation  │
└──────────────┬───────────────────────────────────┘
               │  SSA apply → Deployment, RBAC, CRD
               │  SSA apply → AIGateway CR
               ▼
┌──────────────────────────────────────────────────┐
│  ai-gateway-operator  (module operator)          │
│                                                  │
│  Watches AIGateway CR, manages its               │
│  sub-components (see architecture.md)            │
└──────────────────────────────────────────────────┘
```

## 2. Manifest packaging

ai-gateway-operator publishes its own deploy manifests as a kustomize tree under `config/manifests/ai-gateway-operator/`, with platform overlays at `overlays/odh` and `overlays/rhoai`. The module-handler framework supports both Helm charts (`ChartDir`) and kustomize manifests (`ManifestDir`); this module uses `ManifestDir`, so no Helm chart is generated.

During opendatahub-operator's build, `get_all_manifests.sh` downloads this repo's whole `config/` tree at a pinned commit SHA into the opendatahub-operator container image at `/opt/manifests/aigateway/`. The whole tree is pulled (not just the overlay dir) because the overlay references the operator's shared `crd`/`rbac`/`manager` via relative paths.

At runtime, the modules controller renders the platform-specific overlay (`manifests/ai-gateway-operator/overlays/{odh,rhoai}`) via kustomize and applies it via SSA. The operator image is parameterized through `manifests/ai-gateway-operator/base/params.env` (`AI_GATEWAY_OPERATOR_IMAGE`), which opendatahub-operator substitutes with the digest-pinned reference at deploy time. The batch-gateway operand images the operator passes through at runtime are injected as `RELATED_IMAGE_*` environment variables into the operator Deployment (the module handler's `RelatedImages` mechanism).

## 3. Reconciliation flow

### 3.1 User creates a DataScienceCluster CR
1. User creates a `DataScienceCluster` (DSC) CR with **aigateway** set to `Managed`:

```yaml
apiVersion: datasciencecluster.opendatahub.io/v2
kind: DataScienceCluster
metadata:
  name: default-dsc
spec:
  components:
    aigateway:
      managementState: Managed
      batchGateway:
        managementState: Managed
      maas:
        managementState: Managed
```

### 3.2 opendatahub-operator → ai-gateway-operator
2. opendatahub-operator watches the `DataScienceCluster` CR and sees `aigateway` as `Managed`.
3. opendatahub-operator renders the ai-gateway-operator kustomize manifests (bundled under `/opt/manifests/` in its image) and deploys it via SSA:

```bash
$ oc get deployment -n opendatahub -l app.kubernetes.io/name=ai-gateway-operator
NAME                  READY   UP-TO-DATE   AVAILABLE
ai-gateway-operator   1/1     1            1
```

4. opendatahub-operator creates the `AIGateway` CR. Each managed sub-component is toggled independently via its own field (e.g. `batchGateway`, `maas`):

```yaml
apiVersion: components.platform.opendatahub.io/v1alpha1
kind: AIGateway
metadata:
  name: default-aigateway
spec:
  batchGateway:
    managementState: Managed
  maas:
    managementState: Managed
```

### 3.3 Status aggregation back to the DSC
5. ai-gateway-operator reconciles the `AIGateway` CR and its sub-components, then reports status (`Ready` / `ProvisioningSucceeded` / `Degraded` and `observedGeneration`) on the `AIGateway` CR — see [architecture.md](architecture.md) for how that status is computed.
6. opendatahub-operator reads the `AIGateway` CR status and aggregates it into the `DataScienceCluster` status.

## 4. References

- [FeatureRefinement - RHAISTRAT-1064 - Implement Modular Architecture for ODH Operator](https://docs.google.com/document/d/1qGvaUsioOXl1MPm0TqSxaYR6booRyDLxz_-wTYVF8hM/edit?tab=t.3mrf1syv46a)
- [Onboarding Guide for ODH Operator Modules](https://docs.google.com/document/d/1FgN_U-6XH8M-Mu6XNeldUlTPsnw7UyPCWg5NVJJdYnw/edit?usp=sharing)
- [Module Handler Developer Guide](https://gitlab.cee.redhat.com/data-hub/odh-modularisation-docs/-/blob/main/Module%20Handler%20Developer%20Guide.md?ref_type=heads)
- [opendatahub-module-operator](https://github.com/lburgazzoli/opendatahub-module-operator)
- [odh-platform-utilities](https://github.com/opendatahub-io/odh-platform-utilities)
