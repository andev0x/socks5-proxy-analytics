# Phase 1 — Network Observability: Implementation Report and Plan

**Status:** Phase 1 foundation implemented and verified with the limitations below  
**Baseline commit:** `b19ff66` (branch `main`, pre-change working tree)  
**Architecture baseline:** [`docs/socks5-proxy-analytics-architecture.md`](socks5-proxy-analytics-architecture.md)  
**Report date:** 2026-10-29

This document is the architecture-to-code map, decision log, work plan and evidence record
for Phase 1. Architecture intent is not treated as proof that the runtime behaves that way;
claims below cite commands or tests that were actually run.

## 1. Baseline evidence (before changes)

| Command | Result |
| --- | --- |
| `go build ./...` | pass |
| `go vet ./...` | pass |
| `go test ./...` | pass; only `internal/pipeline` and `internal/security` had tests |
| `go test -race ./...` | pass for those two packages |
| `golangci-lint run ./...` | 2 pre-existing findings (`goconst`, `staticcheck` SA4023) |
| `go version` | `go1.27.3 darwin/arm64` (module declares `go 1.25.5`) |
| PostgreSQL | Homebrew PostgreSQL 18.6 reachable locally |
| Docker | CLI + Compose v5.6.1 available, **daemon not running** |
| Working tree | modified `.gitignore` (`.ohgo/`) and untracked evaluation/architecture docs, preserved |

## 2. Architecture-to-code map

Status after Phase 1. "Verified by" names the executable evidence.

| Architecture responsibility | Actual code | Status | Verified by |
| --- | --- | --- | --- |
| SOCKS5 listener / protocol handler | `internal/proxy/server.go`, `handshake.go` | **confirmed**: CONNECT/TCP, IPv4/IPv6/domain, RFC 1929 optional auth | `TestCONNECT*`, runtime `curl -x socks5h` |
| Client authentication in live path | `security.Authenticator` used by handshake | **confirmed** | `TestAuthenticationIsEnforced`, runtime auth check |
| IP/CIDR client allowlist | `security.IPWhitelist`, checked at accept | **confirmed** | `TestIPWhitelistRejectsBeforeHandshake` |
| Connection limit | `Server` active counter, reserve at accept | **confirmed** | `TestMaxConnectionsEnforced` |
| Per-source-IP rate limit | `security.RateLimiter`, bounded map | **confirmed** (new connections) | `TestRateLimitEnforcedPerClient`, limiter unit tests |
| Client address attribution | `session.clientIP/clientPort` from listener peer | **confirmed** | E2E event assertions |
| Requested hostname before DNS | parsed from ATYP domain and stored separately; dial uses hostname | **confirmed** | `TestCONNECTPreservesAttribution...` (`localhost`) |
| Resolved destination IP | `upstream.RemoteAddr()` after connect | **confirmed** | E2E event/DB row |
| Byte directions | explicit counters around relay writes | **confirmed**, handshake excluded | controlled 13-byte/8-byte payload test |
| Outcomes / termination reasons | one event per admitted session; typed failures | **confirmed** | auth/dial/handshake/shutdown tests |
| Event lifecycle ownership | proxy emits after relay; collector owns bounded channel | **confirmed** | pipeline/proxy tests, `-race` |
| Queue capacity / overload | `pipeline.Collector` non-blocking drop + metric | **confirmed best-effort** | `TestCollectorDropsWhenQueueFull` |
| Normalizer | worker pool, drains until input closed, closes output | **confirmed** | `TestNormalizerMapsEventAndClosesOutput` |
| Publisher batching/retry | atomic batch, bounded retries/backoff, final flush, loss counters | **confirmed** | publisher tests, shutdown test |
| Storage/schema | GORM `AutoMigrate`, explicit fields, unique `event_id`, idempotent batch | **confirmed** with migration caveat | PostgreSQL integration test |
| Analytics API | Gin handlers, validated ranges, DB health | **confirmed** | handler tests, runtime curl |
| Prometheus metrics endpoint | dedicated registry, `/metrics` + `/healthz` HTTP server | **confirmed** | metrics tests, runtime scrape |
| Structured logs | startup config (no secrets), accepts/rejects, failures, shutdown | **confirmed** | runtime logs inspected |
| Graceful shutdown | stop accept → bounded grace → drain → flush → close | **confirmed** (bounded; forced timeout reported) | shutdown tests, `-race`, runtime SIGTERM |
| Docker Compose | env-driven DB host, loopback ports, build context fixed, healthchecks | **static validation pass; runtime blocked** | `docker compose config` (no daemon) |
| CI Go version | module/Docker/CI aligned at `1.26.0` after the security upgrade | **confirmed** (config reviewed) | file inspection; CI not executed here |
| Tests / benchmarks | unit, component/E2E, PostgreSQL integration, benchmarks | **confirmed with scope gaps** | commands in §5 |

## 3. Decisions

### D1 — Event delivery is best-effort telemetry, not durable audit

Evidence: in-memory bounded channels and PostgreSQL batching; no durable spool/replay.

Contract:
- bounded queue (`PIPELINE_BUFFER_SIZE`), non-blocking enqueue;
- saturation drops the event, increments `pipeline_events_dropped_total{stage="collector"}`
  and logs at a bounded rate;
- batches are atomic and idempotent (`event_id` unique, `ON CONFLICT DO NOTHING`), retried a
  bounded number of times with exponential backoff;
- exhausted retries increment `pipeline_publish_failures_total` and
  `pipeline_events_publish_dropped_total`; they are never counted as published;
- shutdown drains and flushes the final batch under bounded deadlines;
- metrics distinguish accepted, persisted and dropped.

Consequence: analytics can be incomplete under overload or database outage. This is
documented in the README and is not described as an audit trail.

### D2 — PostgreSQL is a required startup dependency in Phase 1

The proxy and API run migrations and construct the repository at boot; missing/invalid DB
configuration fails startup with an actionable error. Runtime database failures do not stop
forwarding, but degrade analytics with visible loss. A DB-less mode is a future product
decision.

### D3 — Replace `armon/go-socks5` with an in-repo CONNECT handler

Evidence: the 2016 library's dial/relay hooks only receive an already-resolved address; they
do not expose the SOCKS5 peer or original FQDN, which Phase 1 must attribute correctly. Error
classification is string-based.

Choice: a small handler in `internal/proxy` supporting RFC 1928 CONNECT over TCP, NO AUTH and
RFC 1929 username/password, IPv4/IPv6/domain. BIND and UDP ASSOCIATE return command-not-
supported. Consequences: protocol parsing is owned and unit/E2E tested; BIND/UDP are
explicitly unsupported; dependency removed. No Phase 2 policy logic added.

### D4 — Event field semantics

- `client_ip`, `client_port`: SOCKS5 TCP peer (never upstream).
- `requested_host`: exact hostname from the request; empty for IP literals.
- `destination_ip`: IP actually dialed, from the established socket.
- `bytes_client_to_upstream` / `bytes_upstream_to_client`: post-reply application payload
  only; handshake bytes excluded.
- `outcome`: `connected`, `dial_failed`, `auth_failed`, `handshake_failed`.
- `termination_reason`: bounded enum (`client_close`, `upstream_close`, `relay_error`,
  `server_shutdown`, `dial_error`, auth/handshake failure reasons).
- `timestamp` = accept time; `duration_ms` = accept→finish; `dial_latency_ms` = TCP connect.
- Pre-handshake rejects (connection limit, rate limit, allowlist) are metrics/logs only; they
  have no destination and do not fabricate events.

### D5 — Safe local defaults

Proxy/API/metrics listeners default to `127.0.0.1`. Docker Compose binds `0.0.0.0` inside
containers and publishes loopback ports only. Auth enabled with empty credentials fails
startup; malformed allowlist entries fail startup. Non-loopback proxy bind without auth logs
a warning.

### D6 — Metrics scope and labels

One registry per process, served by the proxy process at `/metrics` (default
`127.0.0.1:9090`) with `/healthz`. Labels are bounded enums (reason/direction/stage/result).
No hostnames, IPs, credentials or request IDs. All label series are initialized at zero so
the endpoint is scrapeable before activity.

### D7 — API changes

Explicit schema fields replace ambiguous ones. Routes: `/stats/top-destinations`,
`/stats/top-client-ips`, `/stats/traffic`, `/logs/traffic`; legacy `/stats/top-domains` and
`/stats/source-ips` remain aliases with corrected fields. Limits are bounded (1–1000),
offsets non-negative, RFC3339 ranges limited to 90 days, DB-backed `/health`. API remains
unauthenticated and loopback-bound by default.

### D8 — Legacy schema compatibility

`AutoMigrate` adds new columns but does not drop old ones. Old rows are treated as schema
version 0; their `source_ip` may actually contain the old (incorrect) upstream address. No
versioned migration or retention policy exists yet. This is a documented upgrade caveat, not
a silent compatibility promise.

### D9 — Security dependency and Go toolchain upgrade

`govulncheck` on the baseline dependency set reported reachable advisories in
`github.com/jackc/pgx/v5`, `github.com/quic-go/quic-go`, `golang.org/x/net` and
`golang.org/x/text`. They were upgraded to fixed releases (`pgx v5.9.2`, `quic-go v0.64.1`,
`x/net v0.60.0`, `x/text v0.42.0`). The required module language version moved from
`go 1.25.5` to `go 1.26.0`, so CI and builder images were aligned to 1.26.0. This is a
compatibility change justified by security fixes; the local benchmark host used Go 1.27.3.

## 4. Work items

| # | Item | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Config loading/validation/env overrides/safe defaults | complete | `internal/config` tests |
| 2 | Auth, allowlist, connection/rate limits enforced live | complete | security/proxy tests, runtime auth |
| 3 | SOCKS5 CONNECT parsing, replies, auth, relay | complete | proxy tests |
| 4 | Correct event attribution/outcome/bytes | complete | proxy tests, runtime DB row |
| 5 | Metrics registry/endpoint wired to runtime | complete | metrics tests, runtime scrape |
| 6 | Bounded queue/drop/batch/retry/final flush | complete | pipeline tests |
| 7 | Lifecycle and shutdown ordering | complete (bounded) | shutdown tests, `-race` |
| 8 | Storage schema/queries and integration test | complete | PostgreSQL integration test |
| 9 | API validation/timeouts/corrected endpoints | complete | handler tests |
| 10 | Compose/Docker/CI alignment and docs | complete except Docker runtime (no daemon) | `docker compose config`, review |
| 11 | Reproducible benchmarks | complete | `docs/benchmarks.md` |
| 12 | Truthful README/docs | complete | README, ENV_SETUP, benchmark doc |

## 5. Verification evidence (actual results)

| Check | Result |
| --- | --- |
| `gofmt -l .` | no output |
| `go build ./...` | pass |
| `go vet ./...` | pass |
| `go test ./...` | pass: config, handlers, metrics, pipeline, proxy, security, storage |
| `go test -race ./...` | pass for all packages with tests (including PostgreSQL integration when `TEST_DB_*` set) |
| `go test -cover ./...` | config 79.7%, handlers 75.9%, logger 58.3%, metrics 96.1%, pipeline 93.0%, proxy 79.4%, security 90.7%; storage integration covered when env is set; `cmd/*` and models have no direct tests |
| `golangci-lint run ./...` (v2.15.0, v2 config) | 0 issues |
| PostgreSQL integration (`TEST_DB_*`, local PG 18.6) | pass: migrate, idempotent batch, destination/client/stats/logs queries, ping |
| `docker compose -f deployments/docker-compose.yml config` | pass; build context and loopback ports validated |
| Docker image build/run | **blocked**: Docker daemon unavailable |
| Runtime E2E | pass: `curl -x socks5h` fetched content via `localhost` domain; event persisted with `client_ip=127.0.0.1`, `requested_host=localhost`, `destination_ip=127.0.0.1`, bytes 87/198; API returned same record; `/metrics` counters moved; SIGTERM logged `clean=true` and exit 0 |
| Runtime auth E2E | no credentials → curl exit 97; wrong credentials → exit 97 with SOCKS auth rejection; correct credentials → content served; metrics `auth_failures_total=1`, handshake reasons `auth_failed=1`, `no_acceptable_auth=1`; auth-failed events persisted without destination |
| Legacy-schema upgrade | pass: created old `traffic_logs` shape without new columns, started the proxy, `AutoMigrate` added the new columns, old row remained readable (schema v0), a new proxied request persisted with correct attribution, SIGTERM clean |
| Benchmarks | `go test -run '^$' -bench . -benchmem -count=3 ./internal/proxy ./internal/pipeline`; results in §6 and `docs/benchmarks.md` |
| `gosec ./...` | pass after a documented `#nosec G101` suppression on the environment-variable name map (names, not credential values) |
| `govulncheck ./...` | **No vulnerabilities found** in reachable code after upgrading pgx/quic-go/x-net/x-text; 2 imported-package and 1 module advisory remain unreachable (reported separately by the tool) |

## 6. Benchmark summary

Environment: Apple M1 Pro (8 cores), macOS 27.0.1, Go 1.27.3 darwin/arm64, loopback.

| Benchmark | Three runs | Allocation |
| --- | --- | --- |
| SOCKS5 relay echo, 64 KiB round trip | 87,163 / 91,316 / 96,615 ns/op; 752 / 718 / 678 MB/s | 0 B/op, 0 allocs/op |
| Collector enqueue | 72.29 / 72.39 / 72.27 ns/op | 0 B/op, 0 allocs/op |
| Event normalization | 4.668 / 4.673 / 4.684 ns/op | 0 B/op, 0 allocs/op |
| Publisher flush, 100 records, fake repo | 398.8 / 396.9 / 396.8 ns/op | 336 B/op, 5 allocs/op |

These are baselines, not targets. The publisher figure excludes PostgreSQL. Details and
caveats: [`docs/benchmarks.md`](benchmarks.md).

## 7. Phase 1 exit criteria

| Criterion | Result | Evidence |
| --- | --- | --- |
| Real SOCKS5 client reaches controlled upstream | **pass** | proxy E2E tests; runtime curl through socks5h |
| Client address, requested host, resolved IP, byte directions tested | **pass** | 13/8-byte payload assertions; runtime DB row |
| Advertised security controls enforced | **pass** | auth/allowlist/limit/rate tests; runtime auth |
| Documented metrics endpoint reachable and reflects real activity | **pass** | metrics tests; runtime `/metrics` before/after |
| Queue overload and persistence failure documented and tested | **pass** (best-effort contract) | collector/publisher tests; README |
| Shutdown handles pending events per contract | **pass** (bounded) | pipeline flush test, active-session shutdown test |
| Clean checkout builds/tests/runs from documented steps | **partial** | build/test/run verified locally; clean-clone and Docker runtime not re-run; DB/user provisioning required |
| Benchmarks reproducible | **pass** | fixed command, env and results documented |

## 8. Known limitations and residual risks

- Best-effort analytics; no durable replay, retention policy, or audit guarantee.
- TCP CONNECT only; BIND/UDP rejected. No idle/relay timeout.
- `dial_latency_ms` is integer milliseconds and may be 0 on loopback; histograms use seconds.
- API/metrics are unauthenticated; safety relies on loopback defaults/network boundary.
- PostgreSQL is required at startup; runtime outage loses analytics after retries, by design.
- `AutoMigrate` only; old columns/rows remain and historical attribution may be wrong.
- Docker image build/run and full Compose integration were not executed (no daemon).
- No API rate limiting, no multi-instance durable delivery, no threat-model review;
  production readiness is not claimed.
- Tests exclude `noctx`/`goconst`/`prealloc` in `_test.go` via lint config; production code is
  checked by the full enabled set.
- The `LICENSE` file changed outside this task's edits (copyright text); it was preserved.

## 9. Recommended next work

1. Run the Docker/Compose path on a host with a daemon and add it as required CI evidence.
2. Add a full proxy→pipeline→PostgreSQL automated E2E test using an isolated database.
3. Add versioned migrations and a documented retention policy.
4. Add API authentication/rate limiting or explicitly scope it to a local-only tool.
5. Evaluate idle/relay timeouts and per-destination limits before Phase 2.
6. Add load tests with concurrent sessions and tail latency before any capacity claim.
