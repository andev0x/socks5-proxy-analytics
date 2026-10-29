# Phase 3 — Implementation Report

## Scope

Phase 3 consolidates Phase 1 (network observability) and Phase 2 (controlled egress) into a
release candidate without replacing the existing architecture. Changes are intentionally
small and targeted at security-boundary correctness, configuration safety, and operator
trust.

## Changes implemented

### 1. IPv4-mapped IPv6 canonicalization in policy evaluation

- Files: `internal/policy/policy.go`
- Added `canonicalIP` and `canonicalIPSpec` helpers.
- Applied canonicalization in `isRestricted`, `ipMatches`, `normalizeRuleIP`, and
  restricted CIDR preparation.
- Effect: an IPv4-mapped IPv6 representation such as `::ffff:127.0.0.1` is evaluated as
  `127.0.0.1`, preventing alternate address representations from bypassing IP/CIDR and
  restricted-destination rules.
- Verified by: `TestIPv4MappedIPv6Restricted`, existing policy tests, `go test -race`.

### 2. Policy-enabled startup validation tightened

- File: `internal/config/config.go`
- `validatePolicy` now requires both `proxy.auth.enabled=true` and at least one configured
  workload when `policy.enabled=true`.
- Effect: policy enforcement can no longer be enabled with workload identity disabled or
  absent, preserving fail-closed behavior.
- Verified by: `go test ./internal/config`.

### 3. DNS resolution timeout

- File: `internal/proxy/server.go`
- `resolveCandidates` now uses a context bounded by `proxy.dial_timeout_ms` for DNS lookup.
- Effect: a stalled DNS lookup cannot block authorization indefinitely.
- Verified by: `go build`, proxy tests.

### 4. Credential comparison cleanup

- File: `internal/security/security.go`
- Removed a redundant self-comparison in `AuthenticateWithIdentity`; password validation
  remains constant-time through `crypto/subtle`.
- Verified by: security tests.

### 5. Documentation and release artifacts

- Updated `README.md` Phase 3 status.
- Added operator, consolidation, acceptance-gate, verification, and limitations documents.

## Compatibility

- No database schema change.
- No configuration key removed.
- Policy-enabled deployments that previously relied on disabled authentication or zero
  workloads will now fail startup validation; this is intentional and fail-closed.
