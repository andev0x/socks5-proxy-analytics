# Release Notes — Phase 3 Release Candidate

> **Historical document.** This describes an earlier iteration and may not match the current
> implementation. The current evidence is in [audit/baseline.md](audit/baseline.md),
> [audit/correctness.md](audit/correctness.md), [audit/security.md](audit/security.md),
> [benchmarks.md](benchmarks.md), and the release status is in
> [release-readiness.md](release-readiness.md).


## Supported capabilities

- SOCKS5 TCP CONNECT proxy with IPv4, IPv6, and domain destinations.
- Optional RFC 1929 authentication and workload identity.
- Deterministic default-deny egress policy with exact/suffix host matching, IP/CIDR,
  port ranges, deny precedence, and restricted destination handling.
- Single-resolution DNS authorization with IPv4-mapped IPv6 canonicalization.
- Asynchronous best-effort analytics pipeline with PostgreSQL persistence.
- Prometheus metrics and analytics REST API.
- Graceful shutdown and bounded resource controls.

## Configuration and deployment changes

- Policy-enabled startup now requires authentication and at least one workload.
- DNS lookups are bounded by the configured dial timeout.
- Compose and defaults remain loopback-first.

## Schema migration implications

- No schema change in Phase 3.
- Existing deployments continue to use AutoMigrate.

## Security-related behavior changes

- IPv4-mapped IPv6 addresses are canonicalized during policy evaluation.
- Policy cannot be enabled without workload identity.

## Known limitations

- Best-effort analytics, not durable audit.
- AutoMigrate only; no versioned migrations.
- No retention cleanup, API auth, or runtime policy reload.
- Docker runtime not re-verified in this session.

## Upgrade and rollback considerations

- Back up PostgreSQL before upgrade.
- Roll back by redeploying the previous binary; schema is additive.
