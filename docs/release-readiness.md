# Release Readiness Report

Date: 2026-10-29
Baseline commit: `4c2d8ec8048322bc2276bef9107ee3e709d3eb1a`
Working tree: modified by this audit (uncommitted changes; see `git status`)

This report distinguishes what was verified, what was changed, what was measured, what remains
unverified, and which deployment profiles are supported. It does not declare production
readiness on the strength of a passing test suite.

## 1. Gate results

| Gate | Status | Evidence | Notes |
| --- | --- | --- | --- |
| **A — Functional correctness** | PASS | `go test ./...`, `go test -race ./...`, integration tests, `docs/audit/correctness.md` | No fuzzing; BIND/UDP are rejection-tested only |
| **B — Performance** | PASS (tested profile) | `docs/benchmarks.md`; component + end-to-end results | No long soak; loopback only |
| **C — Reliability** | CONDITIONAL | concurrency scaling, 12 s database outage, forced/clean shutdown, goroutine checks | Multi-hour soak **NOT RUN** |
| **D — Security** | PASS (no unresolved critical/high) | `docs/audit/security.md`; `gosec` 0, `govulncheck` 0 reachable | Two accepted Medium deployment constraints |
| **E — Operability** | PASS | Docker Compose stack built, healthy, proxied a real request, persisted the event, clean SIGTERM | Single-host Compose only |
| **F — Documentation and release** | PASS | README, operations, benchmarks, audit docs updated | No release/tag published (not authorized) |

**Overall: CONDITIONAL.** Usable for local development and controlled internal deployments
under the constraints in §5. Not recommended for general production or untrusted internet
exposure until the items in §6 are addressed.

## 2. What was verified

- SOCKS5 CONNECT for IPv4, IPv6 and domain destinations; RFC 1929 auth success/failure;
  unsupported method/command/address rejection; malformed and truncated input; handshake,
  dial, policy and relay failure paths.
- Attribution: client peer, requested host, actual dialed IP, ports, outcomes and independent
  byte counters, using controlled payloads and a 20-way concurrency test.
- Policy: default deny, workload identity, deny-over-allow, priority, CIDR and canonicalization,
  restricted destinations, fail-closed behavior, and "policy enabled without engine" rejection.
  Domain requests are resolved once and the evaluated IP is the IP dialed.
- Delivery semantics: bounded queues, counted drops, bounded retries, idempotent batch inserts,
  shutdown-deadline abort with loss accounting.
- Lifecycle: idle timeout, client half-close, upstream-close finalization, forced shutdown,
  clean shutdown, goroutine cleanup.
- Deployment: `docker compose` build and startup with a fresh database (concurrent migrations
  from both processes), health checks, a real `socks5h` request through the published port,
  persistence and API read-back, and a clean `SIGTERM`.
- Security scans: `gosec` (0 issues), `govulncheck` (0 reachable), `golangci-lint` (0 issues),
  race detector.
- End-to-end load: 4 KiB echo at ~9.1k conn/s with p99 3.84 ms; 64 KiB upload 459 MB/s;
  64 KiB download 405 MB/s; exact relay byte accounting; 0 failures during a 12 s database
  outage.

## 3. What was changed

Correctness and reliability:

1. Bounded relay finalization when the upstream closes first (fixes a goroutine/descriptor leak).
2. `NewServer` rejects `policy.enabled` without an engine (closes a policy bypass).
3. PostgreSQL advisory lock serializes concurrent `AutoMigrate` (fixes Compose startup failure).
4. Workload-only authentication now validates; workloads require auth.
5. Handshake timeouts are classified as `timeout` instead of malformed input.
6. Publisher aborts retries and cancels the in-flight write at the shutdown deadline; every
   discarded record is counted.
7. `active_connections` uses `Inc`/`Dec` instead of a racing `Set`.
8. API pagination offset capped; per-request database timeout; `/livez` liveness endpoint.
9. Integration tests create isolated throwaway databases (removes a cross-package flake).

Performance:

10. Cached per-direction Prometheus counters in the relay writer (40.7 → 8.2 ns per write).
11. 512-byte handshake reader (4,360 → 776 B/op; 1,004 → 350 ns).
12. O(1) LRU eviction in the rate limiter (186 µs → 293 ns per new client at capacity).
13. Configurable `proxy.relay_buffer_kb` (default 32; 64 measured faster for bulk, at a memory
    cost).
14. New component benchmarks (handshake, upload/download, parsing, event construction,
    accounting, rate limiter) and an end-to-end load harness.

Operability:

15. Compose `stop_grace_period: 30s`, `no-new-privileges`, pinned `alpine:3.22`.
16. CI: formatting check, benchmark-harness build and benchmark compilation check.
17. Removed tracked example-secret files (`.env.txt`, `configs/config_tmp.yml`).
18. Documentation: `docs/audit/*`, `docs/benchmarks.md`, `docs/operations.md`, README.

## 4. What was measured (before → after)

| Metric | Before | After |
| --- | --- | --- |
| Handshake parse | 1,004 ns, 4,360 B/op | 350 ns, 776 B/op |
| Relay accounting per 32 KiB write | 40.7 ns | 8.2 ns |
| Rate limiter, new client at capacity | 185,929 ns | 293 ns |
| 64 KiB echo, single stream | 746 MB/s | 762 MB/s |
| 1 MiB upload, single stream | 2.77 GB/s | 2.77 GB/s (32 KiB default) / 3.17–3.35 GB/s (64 KiB) |
| End-to-end 4 KiB echo, 32 clients | not measured | 9,070 conn/s, p99 3.84 ms, 0 failures |
| End-to-end 64 KiB upload, 16 clients | not measured | 459 MB/s, p99 4.20 ms |
| End-to-end 64 KiB download, 16 clients | not measured | 405 MB/s, p99 3.21 ms |
| Forwarding during 12 s DB outage | not measured | 0 failures, 8,664 conn/s, loss counted |
| Shutdown, no sessions / active sessions | not measured | 0.3 ms clean / 2.05 s forced with 2 s grace |

Full tables, commands and caveats: `docs/benchmarks.md`.

## 5. Supported deployment profiles

| Profile | Supported? | Conditions |
| --- | --- | --- |
| Local development | Yes | Compose stack, loopback ports, development credentials |
| Controlled internal use | Yes | Trusted clients or auth + allowlist; API/metrics on loopback or behind auth; finite idle timeout recommended |
| Limited production | Conditional | Auth enabled; egress policy enabled with private ranges restricted; PostgreSQL backups + retention; monitoring/alerts on drops and DB errors; single instance |
| General production / untrusted internet | No | No API/metrics auth, no TLS termination, no per-source concurrency cap, no multi-instance coordination, no soak evidence |

## 6. Unverified and remaining risks

Unverified:

- Multi-hour or multi-day soak (memory/goroutine/descriptor trend) — **NOT RUN**.
- High-connection-rate behavior on Linux (only macOS loopback measured).
- Real WAN latency, packet loss, MTU and TLS-terminating upstreams.
- A controlled multi-address DNS resolver test for the "evaluate then dial the same IP"
  invariant (verified by inspection, not by a fake resolver).
- Fuzzing of the SOCKS5 parser.
- PostgreSQL failover, replication, PITR and large-table query performance.
- Non-loopback IPv6 clients and IPv4/IPv6 dual-stack listener behavior.
- Container image scanning/SBOM/signature verification.

Remaining risks (accepted, documented in `docs/audit/security.md`):

- Analytics API and metrics have no application authentication (Medium).
- Private and cloud-metadata ranges are not restricted unless configured (Medium).
- Analytics are best-effort; a crash loses queued events (Medium).
- No per-source concurrency cap (Low).
- No automated data retention (Low).
- Username timing side channel; tag-pinned images; fixed 5 s relay linger (Low).

## 7. Recommended next steps (priority order)

1. Add optional authentication (bearer token or mTLS) to the analytics API and metrics
   endpoint, or refuse non-loopback binds without an explicit override.
2. Add a fake-resolver test for multi-address policy evaluation and dialing.
3. Run a 4–24 hour soak with `benchmarks/loadtest` and publish the trend.
4. Add a per-source-IP concurrent-session cap.
5. Add a fuzz target for `readRequest`/`negotiateAuth`.
6. Add retention tooling or a documented cron job with a tested SQL recipe.
7. Pin container images by digest and produce an SBOM in CI.
8. Add a controlled performance-regression job (non-blocking on PRs) with `benchstat` on a
   fixed runner.

## 8. Release procedure

No release was created or published. When authorized:

1. Ensure the working tree is reviewed and committed; tag with `vX.Y.Z`.
2. CI must pass: formatting, build, vet, tests, race, lint, gosec, govulncheck, benchmark
   compilation.
3. Build multi-arch binaries and images (existing `release.yml` / `docker.yml`).
4. Verify checksums and run the Compose smoke test from `docs/operations.md` §3.1.
5. Publish release notes that state the supported profiles from §5 and the risks in §6.
6. Roll back by redeploying the previous image; schema changes are additive.
