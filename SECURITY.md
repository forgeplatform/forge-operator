# Security Policy

## Supported Versions

The latest released minor version receives security fixes. See [CHANGELOG.md](./CHANGELOG.md) for releases.

| Version | Supported |
|---------|-----------|
| 1.0.x | Yes |
| < 1.0 | No  |

## Reporting a Vulnerability

Please report security issues privately to **office@krletron.xyz**.

Do **not** open a public GitHub issue for suspected vulnerabilities.

Include:

- Operator version and Kubernetes version
- Component affected (controller, webhook, CRD validation, OLM bundle)
- Steps to reproduce, or proof-of-concept
- Impact assessment (privilege escalation in-cluster, cross-tenant data leak, etc.)
- Suggested remediation if you have one

## Disclosure Timeline

- **48 hours** — acknowledgement of report
- **7 days** — initial assessment and severity classification
- **30 days** — fix released or mitigation provided for critical/high severity
- **90 days** — public disclosure after fix is available

We will credit you in the release notes unless you prefer to remain anonymous.

## Scope

In scope:

- forge-operator (this repository) — controllers, webhooks, RBAC bundled with the operator
- CRDs and admission validation logic
- `forgeapi.ClientPool` credential handling and multi-cluster routing
- OLM bundle manifests in `bundle/`

Out of scope:

- Vulnerabilities in upstream controller-runtime, operator-sdk, or k8s.io libraries
- Issues caused by overly broad user-supplied RBAC bindings
- Self-inflicted misconfiguration of `ForgeInstance` credentials
