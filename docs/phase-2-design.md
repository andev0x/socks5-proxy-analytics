# Phase 2 — Controlled Egress Gateway: Design

## Identity model

- Workloads are configured in `proxy.workloads` (YAML) or legacy single credentials.
- Each workload has a stable ID and SOCKS5 username/password.
- Authentication resolves a connection to a workload ID stored in the session and audit event.
- Credentials are never logged or emitted as metrics.

## Policy model and precedence

- Source of truth: validated application configuration (YAML/env), loaded at startup.
- Policy is an immutable snapshot activated atomically in `policy.Engine`.
- Default action is always deny.
- Rules support workload, host exact/suffix, IP/CIDR, port or port range, priority.
- Deny rules take precedence over allow rules; ties broken by priority then config order.
- Restricted destination ranges (loopback/link-local/etc.) require an allow rule that explicitly
  covers the resolved IP.

## Enforcement boundary

- Authorization occurs in `proxy.Server.handle` before `dialUpstream`.
- Domain names are resolved once; policy is evaluated against the resolved IP.
- The dialed IP is the resolved IP chosen during authorization (no TOCTOU re-resolution).
- Unauthorized requests receive SOCKS5 reply 0x02 (not allowed) and emit an audit event.

## DNS and destination security

- Domain resolution happens before policy evaluation.
- If resolution fails, the request is denied and classified as DNS failure.
- A hostname resolving only to restricted or non-allowed IPs is denied.
- IPv4-mapped IPv6 addresses are parsed by `net.ParseIP` and evaluated consistently.
- Suffix matching enforces label boundaries (`example.com` does not match `notexample.com`).

## Audit event lifecycle and delivery

- Policy decisions are fields on the existing best-effort connection event pipeline.
- Denied requests still emit events with outcome `policy_denied`.
- Delivery remains best-effort telemetry with bounded queues, retries, and visible loss.
- Audit fields: workload_id, policy_decision, policy_reason, policy_id, policy_version.

## Policy activation and failure behavior

- Policy is loaded at startup; failure to load aborts startup (fail-closed).
- Missing policy snapshot denies all requests.
- Unknown identity denies.
- No fallback to unrestricted access.

## Configuration and deployment

- `POLICY_ENABLED` toggles enforcement.
- Workloads configured under `proxy.workloads`.
- Rules configured under `policy.rules`.
- Restricted CIDRs configurable via `policy.restricted_cidrs`.

## Security boundaries and known limitations

- API and metrics remain unauthenticated; loopback defaults are the boundary.
- No runtime policy reload; changes require restart.
- Best-effort audit delivery; not durable.
- No retention cleanup job yet.
- AutoMigrate remains schema mechanism.