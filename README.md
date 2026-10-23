# ai-gateway-operator

A Kubernetes operator that manages AI Gateway components for [Open Data Hub](https://opendatahub.io/). It runs as a module operator under the [opendatahub-operator](https://github.com/opendatahub-io/opendatahub-operator), reconciling the `AIGateway` custom resource to deploy and manage sub-components such as [batch-gateway-operator](https://github.com/opendatahub-io/batch-gateway-operator).

## Prerequisites

- Go 1.25+
- A Kubernetes cluster (Kind, CRC, or OpenShift)
- `kubectl` configured to access the cluster

## CI and dependency updates

- GitHub Actions CI runs `govulncheck` on every push and pull request. The job is temporarily non-blocking for remaining Go stdlib findings until a newer toolchain patch is available in product base images; module dependency bumps are expected to keep non-stdlib findings clear.
- [Dependabot](https://docs.github.com/en/code-security/dependabot) is configured via `.github/dependabot.yml` to open weekly pull requests for Go module and GitHub Actions updates.

## Documentation

- **[Enabling Models as a Service](docs/enabling-models-as-a-service.md)** - Deploy and manage MaaS for multi-tenant model inference
- **[Architecture](docs/architecture.md)** - How ai-gateway-operator manages sub-components

## Updating Component Manifests

This operator vendors manifests from sub-components into `config/manifests/`. These files are checked into the repository because the container build expects them in the source tree (`COPY config/manifests/` in the Dockerfile). Manifests can't be fetched at build time because downstream builds are isolated from network.

A daily GitHub Actions workflow (`.github/workflows/update-manifest-shas.yml`) bumps those SHAs to the tip of `main` on each upstream, refreshes the trees, and opens a PR. When bumping manually, commit **both** the pin change and the refreshed trees:

```bash
make update-refs-shas   # rewrite pins to latest commits on main branch
make get-manifests      # download into config/manifests/<component>/
```

`config/manifests/ai-gateway-operator/` is this operator's own deploy tree for opendatahub-operator (not a sub-component pin).

## References

- [Konflux CI Build Onboarding](https://redhat.atlassian.net/browse/RHOAIENG-65493) — RHOAIENG-65493
- [FeatureRefinement - RHAISTRAT-1064 - Implement Modular Architecture for ODH Operator](https://docs.google.com/document/d/1qGvaUsioOXl1MPm0TqSxaYR6booRyDLxz_-wTYVF8hM/edit?tab=t.3mrf1syv46a)
- [Onboarding Guide for ODH Operator Modules](https://docs.google.com/document/d/1FgN_U-6XH8M-Mu6XNeldUlTPsnw7UyPCWg5NVJJdYnw/edit?usp=sharing)
- [Module Handler Developer Guide](https://gitlab.cee.redhat.com/data-hub/odh-modularisation-docs/-/blob/main/Module%20Handler%20Developer%20Guide.md?ref_type=heads)
