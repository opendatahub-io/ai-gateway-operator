# ai-gateway-operator

A Kubernetes operator that manages AI Gateway components for [Open Data Hub](https://opendatahub.io/). It runs as a module operator under the [opendatahub-operator](https://github.com/opendatahub-io/opendatahub-operator), reconciling the `AIGateway` custom resource to deploy and manage sub-components such as [batch-gateway-operator](https://github.com/opendatahub-io/batch-gateway-operator).

## Prerequisites

- Go 1.25+
- A Kubernetes cluster (Kind, CRC, or OpenShift)
- `kubectl` configured to access the cluster

## Documentation

- **[Enabling Models as a Service](docs/enabling-models-as-a-service.md)** - Deploy and manage MaaS for multi-tenant model inference
- **[Architecture](docs/architecture.md)** - How ai-gateway-operator manages sub-components

## Updating Component Manifests

This operator vendors manifests from sub-components into `config/manifests/`. These files are checked into the repository because the container build expects them in the source tree (`COPY config/manifests/` in the Containerfile).

When a sub-component's manifests change, update the vendored copy:

1. Edit `hack/scripts/get-manifests.sh` and update the commit SHA for the component.
2. Run `make get-manifests` to fetch the manifests at the pinned commit.
3. Commit both `hack/scripts/get-manifests.sh` and `config/manifests/` changes together.

## References

- [Konflux CI Build Onboarding](https://redhat.atlassian.net/browse/RHOAIENG-65493) — RHOAIENG-65493
- [FeatureRefinement - RHAISTRAT-1064 - Implement Modular Architecture for ODH Operator](https://docs.google.com/document/d/1qGvaUsioOXl1MPm0TqSxaYR6booRyDLxz_-wTYVF8hM/edit?tab=t.3mrf1syv46a)
- [Onboarding Guide for ODH Operator Modules](https://docs.google.com/document/d/1FgN_U-6XH8M-Mu6XNeldUlTPsnw7UyPCWg5NVJJdYnw/edit?usp=sharing)
- [Module Handler Developer Guide](https://gitlab.cee.redhat.com/data-hub/odh-modularisation-docs/-/blob/main/Module%20Handler%20Developer%20Guide.md?ref_type=heads)
