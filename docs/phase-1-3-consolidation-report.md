# Phase 1–3 Consolidation Report

## Product evolution

| Capability | Phase introduced | Current implementation | Verification evidence | Limitation |
|---|---|---|---|---|
| SOCKS5 TCP CONNECT | Phase 1 | `internal/proxy` in-repo implementation | Proxy tests, E2E tests | TCP CONNECT only; BIND/UDP ASSOCIATE rejected |
| IPv4/IPv6/domain destinations | Phase 1 | Handshake ATYP handling | Proxy tests | None beyond address-family edge cases |
| SOCKS5 username/password auth | Phase 1 | RFC 1929 handshake + `security.Authenticator` | `TestAuthenticationIsEnforced` | No external identity provider |
| Client allowlist and rate limit | Phase 1 | `security.IPWhitelist`, `security.RateLimiter` | Unit and proxy tests | In-process state only |
| Connection limits and timeouts | Phase 1 | `Server.admit`, handshake/dial timeouts | Tests | No global fairness guarantees |
| Attribution and byte accounting | Phase 1 | `session` + relay counters | `TestCONNECTPreservesAttributionAndByteDirections` | Handshake bytes excluded by design |
| Analytics pipeline | Phase 1 | Collector/normalizer/publisher | Pipeline tests | Best-effort, bounded queues |
| PostgreSQL persistence | Phase 1 | GORM `AutoMigrate`, repository | Storage integration test | No versioned migrations |
| Analytics API | Phase 1 | Gin handlers | Handler tests | Unauthenticated, loopback default |
| Metrics endpoint | Phase 1 | Dedicated Prometheus registry | Metrics tests, scrape | Unauthenticated, loopback default |
| Health/readiness | Phase 1 | `/healthz` liveness, `/health` DB readiness | Handler/tests | Proxy process has no DB readiness endpoint |
| Workload identity | Phase 2 | `WorkloadConfig` + authenticator | Policy enforcement test | Restart required for changes |
| Deterministic policy engine | Phase 2 | `internal/policy.Engine` | Policy unit tests | No runtime reload |
| DNS single-resolution enforcement | Phase 2 | `evaluatePolicy` resolves once | Enforcement test | DNS timeout now bounded |
| Restricted destination handling | Phase 2 | `Policy.isRestricted` | `TestRestrictedDestination` | Depends on configured CIDRs |
| IPv4-mapped IPv6 handling | Phase 3 | Canonicalization in policy | `TestIPv4MappedIPv6Restricted` | Host firewall still required for full isolation |
| Policy-enabled config safety | Phase 3 | Strict validation | Config tests | Legacy single-auth policy unsupported |
| Best-effort audit semantics | Phase 1–3 | Documented delivery guarantee | Pipeline tests | Not durable |

## Consolidation conclusion

Phase 1 and Phase 2 capabilities are present in the current implementation and covered by
unit, component, and available integration tests. Phase 3 added targeted security-boundary
and configuration-safety fixes plus operational documentation. No broad rewrite or database
replacement was performed.
