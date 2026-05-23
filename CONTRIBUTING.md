# Contributing to forge-operator

Thanks for your interest in contributing!

The full contributing guide — git workflow, commit conventions, coding standards, PR process — lives in the [forge-deploy repository](https://github.com/forgeplatform/forge-devops/blob/main/docs/10-contributing-guide.md). Please read it before submitting a pull request.

## What lives here

Kubernetes operator that reconciles Forge resources (`Organization`, `Team`, `Project`, `Workflow`, `Inventory`, `Credential`, `JobTemplate`, `Schedule`, `ForgeInstance`) against one or more Forge API endpoints.

Built with [operator-sdk](https://sdk.operatorframework.io/) and [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime). Distributed via Helm chart and OLM bundle.

## Quick start

```bash
git clone https://github.com/forgeplatform/forge-operator.git
cd forge-operator
make install         # install CRDs into current kube context
make run             # run controller locally
```

Tests:

```bash
make test            # envtest-based controller tests
make bundle-validate # OLM bundle validation
```

## Operator-specific guidelines

- **CRD changes** — `make manifests` to regenerate CRDs and RBAC. Both `config/crd/bases/` and `helm/crds/` must stay in sync.
- **OLM bundle** — every CRD change requires a CSV update (alm-examples, descriptor metadata). Run `make bundle && operator-sdk bundle validate ./bundle --select-optional name=operatorhub`.
- **envtest** — new controllers/webhooks need lifecycle tests using `envtest` (Kubernetes API simulator).
- **Multi-cluster routing** — when modifying `forgeapi.ClientPool`, preserve cache invalidation on `spec.forgeInstance` change.
- **Version bumps** — operator version, Chart.yaml, helm values `tag`, and CSV version must all agree.

## Reporting bugs

Open an issue with reproduction steps, operator + Kubernetes versions, and reconcile logs (`kubectl logs -n forge-operator`).

For security vulnerabilities, see [SECURITY.md](./SECURITY.md) — please do **not** open a public issue.
