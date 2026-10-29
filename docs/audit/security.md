# Security and Resilience Audit (Phase 5)

Scope: the actual implementation at `4c2d8ec` plus the changes made in this audit. This is a
threat-focused code and configuration review with executed checks. It is **not** a penetration
test or a security certification.

## 1. Checks executed

| Check | Command | Result |
| --- | --- | --- |
| Static analysis | `gosec ./...` | 0 issues (20 files, 4,610 lines) |
| Reachable vulnerabilities | `govulncheck ./...` | 0 reachable; 1 vulnerability in a required module is not called |
| Linting | `golangci-lint run` | 0 issues |
| Race detector | `go test -race ./...` | pass |
| Auth failure paths | `TestAuthenticationIsEnforced`, `TestAuthenticationRejectsClientWithoutUserPassMethod` | pass |
| Policy fail-closed | `TestPolicyDefaultDenyWithoutMatchingRule`, `TestPolicyEnabledWithoutEngineIsRejected` | pass |
| Rate-limit / connection-limit bypass | `TestRateLimitEnforcedPerClient`, `TestMaxConnectionsEnforced`, `TestIPWhitelistRejectsBeforeHandshake` | pass |
| Malformed input | `TestMalformedGreetingIsReported`, `TestMalformedRequestVariants`, `TestTruncatedAuthIsReported` | pass |
| Resource retention | `TestUpstreamCloseFinalizesSessionWithoutClientClose`, idle-timeout test | pass |
| Database outage | 12 s PostgreSQL stop during a 40 s load run | forwarding continued, loss counted, recovery verified |
| Secret exposure in logs | inspection of every log call; `gosec` G101 suppression is a variable-name list | no credential values logged |

`govulncheck -show verbose` reports one module-level advisory that this code does not reach.
It is not a finding for this product but should be re-checked on dependency updates.

## 2. Findings

Severity reflects impact in the documented deployment model (loopback or authenticated,
trusted-network client). A deployment that exposes the proxy or API to an untrusted network
inherits higher impact for the access-control findings.

### F-1 — Policy enforcement could be bypassed by construction (High, fixed)

**Evidence.** `proxy.NewServer` accepted `cfg.Policy.Enabled == true` together with a `nil`
`*policy.Engine`. `Server.isPolicyEnabled()` required both, so the server silently forwarded
every request without evaluating policy. Any future caller or test harness could create this
state. Confirmed by a failing regression test before the fix.

**Remediation.** `NewServer` now returns an error when `policy.enabled` is true and no engine is
provided. Regression test: `TestPolicyEnabledWithoutEngineIsRejected`.

**Residual risk.** The production wiring in `cmd/proxy` always constructs an engine, and
`policy.NewEngineFromConfig` fails closed if the policy is invalid. No bypass remains in the
supported paths.

### F-2 — No authentication on the analytics API or metrics endpoint (Medium, documented)

**Evidence.** `cmd/api` registers read-only routes with no auth middleware. The metrics server
serves `/metrics` and `/healthz` without auth. Both default to `127.0.0.1`, and Compose
publishes them on loopback only.

**Impact.** Anyone who can reach the port can read connection metadata (client IPs, requested
hosts, destinations, byte counts, policy decisions) and operational metrics. The API is
read-only; there is no write path.

**Remediation / disposition.** Documented as a deployment boundary requirement. Production
deployments must place the API and metrics behind a reverse proxy with authentication and TLS,
or bind them to a private interface. Do not publish them publicly. This is an accepted
limitation for the current product scope, not a code defect.

**Residual risk.** A misconfigured bind address (`0.0.0.0`) exposes telemetry without
authentication. Startup logs the configured addresses but does not refuse a non-loopback API
bind. Recommended next step: an optional bearer-token middleware or a startup warning for
non-loopback API/metrics binds.

### F-3 — Per-client rate-limiter eviction was O(n) under a source-IP flood (Medium, fixed)

**Evidence.** `RateLimiter.evictLocked` scanned the entire bucket map (up to
`rate_limit.max_tracked_clients`, default 100,000) and then scanned again to find the oldest
entry, while holding the global limiter mutex. Measured at 186 µs per new client at capacity
(`BenchmarkRateLimiterSaturatedNewClient`). A flood of new source IPs could serialize all
admissions and saturate a CPU core.

**Remediation.** Tracked clients are now kept in a `container/list` LRU; eviction is O(1).
Measured 293 ns per new client at capacity (≈635× faster). Memory remains bounded by
`max_tracked_clients`.

**Residual risk.** Evicting the least-recently-used client resets its token bucket, so an
attacker who can rotate source IPs faster than the table can track them can obtain a fresh
bucket. This is inherent to bounded per-IP state and is documented.

### F-4 — Relay sessions could be retained indefinitely after upstream close (Medium, fixed)

**Evidence.** When the upstream half of the relay finished first, the proxy called
`CloseWrite` on the client and then blocked reading from the client forever. With the default
`proxy.idle_timeout_ms=0`, a client that never closed pinned a goroutine and two file
descriptors. A malicious client could exhaust descriptors.

**Remediation.** After the upstream direction completes, the proxy waits at most 5 s for the
client to close, then closes it. Regression test:
`TestUpstreamCloseFinalizesSessionWithoutClientClose`.

**Residual risk.** A client can still hold a session open for the configured idle timeout (or
indefinitely when `idle_timeout_ms=0`) while data is flowing. Operators exposing the proxy to
untrusted clients should set a finite idle timeout.

### F-5 — Concurrent first-time migrations caused startup failure (Medium, fixed)

**Evidence.** The documented Compose startup runs `AutoMigrate` from both processes against the
same fresh database. Reproduced 5/5 times with
`duplicate key value violates unique constraint "pg_type_typname_nsp_index"` and
`relation "traffic_logs" already exists`. One process would exit and restart until the other
finished.

**Impact.** Availability and operational noise; no data corruption. It also creates a
restart-loop pattern that can hide other startup failures.

**Remediation.** `storage.migrate` takes a PostgreSQL advisory lock (`pg_try_advisory_lock`) on
a dedicated connection, bounded by the database connect timeout, before running
`AutoMigrate`. Regression test: `TestConcurrentMigrations` (creates a throwaway database and
runs four migrations concurrently).

**Residual risk.** Advisory locks are per database cluster and released automatically if the
connection dies. A migration that runs longer than `database.connect_timeout_ms` will time out
waiting for the lock; increase that timeout for large schemas.

### F-6 — Default restricted CIDRs do not cover private networks (Medium, documented)

**Evidence.** `Policy.prepareRestricted` defaults to loopback, `169.254.0.0/16`, `fe80::/10`,
`0.0.0.0/8` and `::/128`. RFC 1918 ranges, `fc00::/7` and cloud metadata addresses outside
`169.254.0.0/16` are not restricted unless configured.

**Impact.** With policy enabled and an allow rule that covers an internal address (for example
a broad CIDR rule), the proxy can reach internal services. An allow rule must explicitly cover
the IP, so this is not an engine bypass, but the default is permissive with respect to private
address space.

**Remediation / disposition.** Documented in `docs/operations.md` and the configuration
reference. Operators who need SSRF containment must set `policy.restricted_cidrs` explicitly
(for example `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `fc00::/7`).

### F-7 — API pagination offset was unbounded (Low, fixed)

**Evidence.** `offset` was any non-negative integer, so a client could force deep `OFFSET`
scans. Remediation: capped at 1,000,000; per-request database timeout of 10 s. Residual risk:
large time ranges still scan many rows; the 90-day window cap and `timestamp` index bound the
cost.

### F-8 — Per-connection handshake buffer was 4 KiB (Low, fixed)

**Evidence.** `bufio.NewReader(conn)` allocated 4,096 bytes per connection for a negotiation
whose largest message is 776 bytes (4,360 B/op measured). At 10,000 new connections/s this is
~43 MB/s of avoidable allocation. Remediation: 512-byte reader (776 B/op, 349 ns/op). This is
a memory-amplification hardening, not a vulnerability.

### F-9 — Docker base image was unpinned (Low, fixed)

**Evidence.** `FROM alpine:latest` in both Dockerfiles made runtime contents non-reproducible.
Remediation: `alpine:3.22`. The builder image remains `golang:1.27.2-alpine`; pin it by digest
for fully reproducible builds. Residual risk: no image digest pinning, no SBOM/signature.

### F-10 — No data retention policy (Low, documented)

**Evidence.** `traffic_logs` grows without bound; there is no deletion job or partition
strategy. Remediation: documented retention/backup guidance in `docs/operations.md`. Residual
risk: disk exhaustion on long-running deployments; operators must schedule retention.

### F-11 — Global connection cap, no per-source concurrency cap (Low, documented)

**Evidence.** `proxy.max_connections` is global; the rate limiter bounds new connections per
source IP but not concurrent sessions. One source IP can occupy all slots if it holds
connections open.

**Residual risk.** Accepted for the current scope. Mitigations: IP allowlist, finite
`idle_timeout_ms`, and a front-end load balancer with per-client limits.

### F-12 — Example credentials were tracked in the repository (Low, fixed)

**Evidence.** `.env.txt` (a duplicate of `.env.example`) was tracked and contained
`DB_PASSWORD=postgres`; `configs/config_tmp.yml` contained a developer-specific database user.
Neither is a production secret, but both invite copy-paste of weak credentials.

**Remediation.** Both files were removed. `.env` and `configs/config.yml` are gitignored.
`configs/config.example.yml` and `.env.example` keep development-only credentials clearly
labeled.

## 3. Network security review

- **Authentication.** Optional RFC 1929; when enabled, all methods other than username/password
  are refused. Passwords are compared with `crypto/subtle`. Username lookup uses a Go map, so
  username existence is observable through timing; this is a low practical risk for a
  credential-based proxy and is documented rather than "fixed" with a costly constant-time map.
- **Allowlist and rate limiting.** The allowlist is checked before the handshake and before any
  resource allocation beyond the accepted socket. Rate limiting is per source IP with a token
  bucket and bounded state. Client IPs are canonicalized, so an IPv4 client on a dual-stack
  listener (`::ffff:127.0.0.1`) shares one allowlist and rate-limit bucket with `127.0.0.1`.
- **Malformed input.** Every protocol read is length-bounded by the wire format; the only
  attacker-controlled allocation is a 255-byte method list and a 255-byte domain. Both are
  bounded by one byte.
- **Deadlines.** Handshake, dial, idle and flush timeouts exist. `idle_timeout_ms=0` disables
  the relay idle timeout by design; this is documented as unsafe for untrusted clients.
- **Relay cancellation.** Closing either side unblocks both copies; the forced-shutdown path
  closes every tracked connection.
- **IPv4/IPv6 consistency.** Policy canonicalizes IPv4-mapped IPv6 to IPv4 before matching and
  before restricted-range checks (`canonicalIP`). Tested in `internal/policy`.
- **Untrusted destinations.** The proxy dials whatever the client requests unless policy is
  enabled. This is the product's purpose; deployments must enable policy to constrain egress.

## 4. API and observability review

- Read-only routes; no request bodies.
- `limit` bounded (1–1000), `offset` bounded (≤1,000,000), time range bounded (≤90 days).
- Server read/write/idle timeouts configured; per-request DB timeout 10 s.
- Database errors are logged server-side; clients receive `{"error":"internal error"}`.
- No high-cardinality metric labels: all label values are bounded enums (`reason`,
  `direction`, `stage`, `result`). Hostnames, IPs and event IDs never appear in labels.
- `/livez` (process) and `/health`/`/readyz` (database) have distinct semantics. The proxy's
  `/healthz` is a liveness check and does not depend on the database, so a database outage does
  not cause a container restart loop.

## 5. Database and data integrity review

- All queries use GORM parameter binding; no string-built SQL with user input.
- `event_id` is unique; batch inserts use `ON CONFLICT DO NOTHING`, so retries are idempotent.
- Batch writes are transactional (`CreateInBatches` inside `Transaction`).
- Connection pool is bounded (`max_open_conns`, `max_idle_conns`, 30-minute lifetime).
- Credentials are passed to the driver as a URL with `url.UserPassword`, so special characters
  are escaped; the GORM logger is set to `Silent` and does not print SQL or parameters.
- Runtime database failure: forwarding continues; events are dropped with counters; the
  publisher retries with bounded backoff and recovers when the database returns (verified).
- Accepted limitation: best-effort telemetry with no durable spool. A crash loses queued
  events.
- Backups and retention are deployment responsibilities and are documented.

## 6. Supply chain review

- `go.sum` is present and `go mod verify` passes in CI.
- `govulncheck` is clean for reachable code.
- Dependabot is configured for Go modules, Docker and GitHub Actions.
- Docker builder images are pinned by tag, not digest; runtime images are now pinned to
  `alpine:3.22` but still by tag. No image signing or SBOM is produced.
- CI actions are pinned to major versions (`@v4`, `@v5`), not commit SHAs.
- Secrets are injected via environment variables; no secrets are baked into images
  (`.dockerignore` excludes `.env*` and `configs/config.yml`).

## 7. Residual risk summary

| Risk | Severity | Disposition |
| --- | --- | --- |
| Unauthenticated API/metrics on a non-loopback bind | Medium | Accepted, deployment boundary + documentation |
| Private/metadata ranges not restricted by default | Medium | Accepted, explicit `restricted_cidrs` configuration |
| Best-effort analytics can lose events on crash | Medium | Accepted product limitation, observable counters |
| No per-source concurrency cap | Low | Accepted, use allowlist/idle timeout/front proxy |
| Unbounded table growth without retention | Low | Accepted, operator responsibility |
| Username timing side channel | Low | Accepted, password comparison is constant-time |
| Tag-pinned (not digest-pinned) images | Low | Accepted, recommend digest pinning for release |
| Relay linger constant not configurable | Low | Accepted, bounded at 5 s |

No unresolved critical or high-severity finding remains. The two Medium items that are not
code defects are explicit deployment constraints and must be satisfied by any production
operator.
