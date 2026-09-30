# Contributing to ai-gateway-operator

Thanks for your interest in contributing. This guide explains the conventions
this repo enforces and how to work with it.

## Table of contents

- [Contributing to ai-gateway-operator](#contributing-to-ai-gateway-operator)
  - [Table of contents](#table-of-contents)
  - [Getting started](#getting-started)
  - [Development setup](#development-setup)
  - [CI and checks](#ci-and-checks)
  - [Testing](#testing)
    - [Test ownership boundaries: AIGO / AIGC / MaaS](#test-ownership-boundaries-aigo--aigc--maas)
  - [Getting help](#getting-help)

## Getting started

1. **Fork** the repository on GitHub.
2. **Clone** your fork and add the upstream remote:
   ```bash
   git clone git@github.com:YOUR_USERNAME/ai-gateway-operator.git
   cd ai-gateway-operator
   git remote add upstream https://github.com/opendatahub-io/ai-gateway-operator.git
   ```
3. **Create a branch** from `main` for your work:
   ```bash
   git fetch upstream
   git checkout -b your-feature upstream/main
   ```
4. Go toolchain version is pinned in [go.mod](./go.mod).

## Development setup

- `make build` — `manifests`, `generate`, `fmt`, `vet` (build manager binary).
- `make run` — build and run the manager locally against your current kube context.
- `make get-manifests` — fetch each managed sub-component's manifests (e.g.
  `maascontroller`, `batchgateway`) from their pinned commit SHAs in
  `hack/scripts/get-manifests.sh` into `config/manifests/<sub-component>/`.
  Commit the result whenever you bump a pinned SHA — see
  [docs/architecture.md](docs/architecture.md#22-ai-gateway-operator-fetches-sub-component-manifests).
- `make manifests` — regenerate `config/rbac/role.yaml` from kubebuilder RBAC
  markers in `aigateway_controller.go`. Run this whenever you add permissions
  needed by a managed sub-component's workload.
- `make deploy` — apply `config/default/` (this operator's own deploy
  manifest) to the current cluster via kustomize.
- `make test` — unit tests.
- `make test-integration` — integration tests against a real API server
  (`prepare-integration` installs CRDs, `test-integration-run` starts an
  in-process manager); needs a cluster already available (CI provisions Kind).
- `make test-e2e` — full e2e: `cleanup-e2e`, `deploy`, `test-e2e-run` against a
  live cluster (CI provisions Kind and runs `make get-manifests` first).
- `make lint` / `make lint-fix` — golangci-lint.

## CI and checks

| Workflow | Trigger | What it checks |
|---|---|---|
| `ci.yml` / `lint` | push + PR | `make lint` |
| `ci.yml` / `test` | push + PR | `make test`, then provisions Kind + `make get-manifests` + `make test-integration` |
| `ci.yml` / `e2e` | push + PR (after `test`) | Provisions Kind, `make get-manifests`, `make test-e2e` |
| `ci-image.yml` | push + PR | Container image build |
| `tls-lint.yml`, `semgrep-tls.yml` | push + PR | TLS-configuration static analysis |

**Run locally before pushing:** `make lint`, `make test`, and — if you touched
reconciliation logic or manifests — `make test-integration` / `make test-e2e`
against a local Kind cluster.

## Testing

This repo has three test layers:

| Layer | Location | What it covers |
|-------|----------|----------------|
| Unit | `internal/**/*_test.go`, `pkg/**/*_test.go` | Reconciler logic against fakes (`controller-runtime/pkg/client/fake`) |
| Integration | `test/integration/integration_test.go`, `test/support/` | `AIGateway` reconciliation against a real API server (envtest-style), no full sub-component workloads |
| E2E | `test/e2e/*_test.go` | Full sub-component deploy against a live Kind cluster |

### Test ownership boundaries: AIGO / AIGC / MaaS

AI Gateway now spans three repos: this one (AIGO), [ai-gateway-controller](https://github.com/opendatahub-io/ai-gateway-controller) (AIGC), and [models-as-a-service](https://github.com/opendatahub-io/models-as-a-service) (MaaS). Where a test belongs depends on what it needs to observe, not which repo you happen to be changing:

| Repo | Owns | What it tests | Test suite |
|------|------|----------------|------------|
| **AIGO** (this repo) | Component setup & dependency management — deploy/status only | `AIGateway` CR reconciles to `Ready`; sibling controller Deployments (`maas-controller`, `ai-gateway-controller`, `batch-gateway-operator`) become `Available`; aggregate status (`ModelsAsAServiceReady`, etc.) rolls up correctly. **Never the request path.** | `test/e2e/*_test.go` — see `maasPrerequisites`/`maasCleanup` in `test/e2e/models_as_service_test.go` for the pattern: install prereqs, create the CR, assert on status. No HTTP calls to a gateway, no route assertions. |
| **AIGC** | Deploying the Praxis-backed stack + AIGC's own control-plane logic | Its own reconciliation logic (`pkg/tenant`, `pkg/render`, `pkg/controller` for ExternalModel/ExternalProvider) via real fixtures. For MaaS-level/request-path behavior (subscription enforcement, auth, route matching, identity headers), AIGC does **not** write new test content of its own — it fetches and re-runs **MaaS's own pytest suite** against its Praxis-backed deployment, pinned via `test/maas-e2e.lock`. | `test/kind-env`, `test/openshift-env` + a vendored, pinned copy of MaaS's `test/e2e/tests/` |
| **MaaS** | The single source of truth for MaaS-level resource/request behavior | Subscription enforcement, auth policy, rate limiting, API-key lifecycle, and anything visible at the MaaS API/Gateway boundary — including OpenAI resource-API routing and identity-header propagation. Written once, run twice: in MaaS against IPP, and via AIGC's vendored fetch against Praxis. | MaaS's `test/e2e/tests/*.py` (pytest) |

**Do not add request-path assertions (route matching, header forwarding, plugin behavior, tenant-isolation-at-the-routing-layer) to this repo's e2e suite.** If a PR against this repo needs that kind of coverage, it almost always belongs in AIGC (if it's about Praxis wiring or AIGC's own reconciliation) or in MaaS (if it's about MaaS resource/request semantics — subscriptions, auth, rate limits). This repo's e2e should only ever need to answer "did the component get installed and report `Ready`?"

**The upstream-first rule (for the AIGC ↔ MaaS boundary):** if MaaS-level behavior has a coverage gap, add the test in MaaS's `test/e2e/tests/` first (skip-gated with `pytest.mark.skipif` if genuinely Praxis-only), then have AIGC bump its pin to pick it up. Don't let a bespoke copy accumulate downstream. [`ai-gateway-controller#37`](https://github.com/opendatahub-io/ai-gateway-controller/pull/37) is a worked example: a gap was fixed upstream in MaaS ([#1508](https://github.com/opendatahub-io/models-as-a-service/pull/1508)); once it landed, AIGC removed its own side-workarounds and re-pinned `test/maas-e2e.lock`.

## Getting help

- **Open an issue** on GitHub for bugs or feature ideas.
- **Design questions:** see [docs/architecture.md](docs/architecture.md) and
  [docs/integration-opendatahub-operator.md](docs/integration-opendatahub-operator.md)
  first; open a discussion or issue if they don't answer your question.
