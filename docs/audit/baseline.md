# Audit Baseline (Phase 0)

This document records the repository state and the verification results that existed **before**
any implementation change in this audit. It is evidence, not a target.

## 1. Repository state

| Item | Value |
| --- | --- |
| Commit | `4c2d8ec8048322bc2276bef9107ee3e709d3eb1a` |
| Branch | `main` |
| Working tree at baseline | clean (`git status --short` produced no output) |
| Module | `github.com/andev0x/socks5-proxy-analytics` |
| Go directive in `go.mod` | `go 1.26.10` |
| Date of baseline | 2026-10-29 |

## 2. Environment

| Item | Value |
| --- | --- |
| OS | macOS 27.0.1 (build 26A434), arm64 |
| CPU | Apple M1 Pro, 8 logical cores |
| Memory | 16 GiB |
| Go toolchain | `go1.27.3 darwin/arm64` |
| PostgreSQL | 15.19 (Debian build, container `postgres:15`) |
| Docker | 29.2.1 |
| golangci-lint | v2.15.0 (built with go1.27.2) |
| gosec | dev build, go1.27.3 |
| govulncheck | v1.9.0, vulnerability DB 2026-10-27 |
| benchstat | not installed (results compared manually) |

The module's `go` directive (1.26.10) is older than the installed toolchain (1.27.3). All
commands below were executed with the installed toolchain; no `toolchain` directive is
present, so a Go 1.26.x toolchain is also expected to build the module.

## 3. Package and process inventory

Two processes share one PostgreSQL database:

- `cmd/proxy` — SOCKS5 listener, Prometheus endpoint, analytics pipeline, database writes.
- `cmd/api` — Gin analytics API, database reads, DB-backed readiness.

Internal packages: `proxy`, `security`, `policy`, `pipeline`, `storage`, `models`, `metrics`,
`handlers`, `config`, `logger`. The repository contained 33 Go files; gosec scanned 20
non-test files / 4,610 lines.

## 4. Commands executed and observed results

All commands were run from the repository root.

| Command | Result |
| --- | --- |
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0, no diagnostics |
| `go test ./...` | exit 0, all packages `ok` |
| `go test -race ./...` | exit 0, all packages `ok` (proxy package 9.8 s) |
| `golangci-lint run` | exit 0, `0 issues` |
| `gosec ./...` | exit 0, `Issues: 0` (20 files, 4,610 lines, 1 `#nosec`) |
| `govulncheck ./...` | exit 0, "No vulnerabilities found"; 1 vulnerability exists in a required module but is not reachable from this code |
| `go test -race` with `TEST_DB_*` | `TestPostgresRepositoryIntegration` PASS, `TestProxyPipelinePostgresE2E` PASS |

Integration tests are skipped unless `TEST_DB_HOST` is set. The baseline run used a disposable
`postgres:15` container on `127.0.0.1:55432` with database `socks5_test`. A skipped integration
test was not counted as a pass.

### 4.1 Baseline component benchmarks

Command:

```bash
go test -run '^$' -bench . -benchmem -count=3 ./internal/proxy ./internal/pipeline
```

Observed (three runs):

| Benchmark | Runs | Allocations |
| --- | --- | --- |
| `BenchmarkSOCKS5RelayEcho` (64 KiB round trip) | 90,849 / 94,786 / 101,692 ns/op; 721 / 691 / 644 MB/s | 0 B/op, 0 allocs/op |
| `BenchmarkCollectorEnqueue` | 74.58 / 75.17 / 74.11 ns/op | 0 B/op, 0 allocs/op |
| `BenchmarkEventNormalization` | 5.55 / 5.57 / 5.51 ns/op | 0 B/op, 0 allocs/op |
| `BenchmarkPublisherBatchPersist` (100 records, fake repo) | 402.7 / 389.6 / 400.9 ns/op | 336 B/op, 5 allocs/op |

The existing benchmark suite covered only these four paths. It had no handshake, policy,
rate-limiter, accounting or end-to-end measurements.

## 5. Defects observed during baseline inspection

These were confirmed by executing tests or by reading the implementation; each is reproduced
in `docs/audit/correctness.md` with the regression test that now covers it.

1. **Session leak when the upstream closes first.** `internal/proxy/session.go` half-closed the
   client and then waited forever for the reverse copy when the client kept its socket open.
   With `proxy.idle_timeout_ms=0` (the default) this pinned a goroutine and two descriptors per
   affected session. Confirmed by a failing test (`TestUpstreamCloseFinalizesSessionWithoutClientClose`).
2. **Policy bypass by construction.** `proxy.NewServer` accepted `policy.enabled=true` with a
   `nil` engine, in which case `isPolicyEnabled()` returned false and every request was
   forwarded. Confirmed by a failing test (`TestPolicyEnabledWithoutEngineIsRejected`).
3. **Concurrent first-time migration failure.** The documented Compose startup runs
   `AutoMigrate` from both the proxy and the API against a fresh database. Reproduced 5/5 times
   with `duplicate key value violates unique constraint "pg_type_typname_nsp_index"` and
   `relation "traffic_logs" already exists`.
4. **Documented workload configuration could not start.** `validateProxyAuth` required legacy
   `proxy.auth.username/password` even when `proxy.workloads` supplied identities, so the
   configuration shown in `docs/operations.md` failed validation.
5. **Rate-limiter eviction was O(n) per new client.** At capacity (100,000 tracked clients) each
   unseen source IP scanned the whole table while holding the global limiter mutex: 186 µs/op
   measured. A source-IP flood could serialize admissions.
6. **Relay byte accounting looked up a Prometheus child counter on every write.** 40.7 ns per
   32 KiB write, on the hottest relay path.
7. **The handshake reader allocated a 4 KiB buffer per connection** (4,360 B/op, 13 allocs/op)
   for a negotiation whose largest message is 776 bytes.
8. **Truncated handshakes were recorded as malformed input** rather than timeouts, so the
   pre-registered `socks5_proxy_handshake_failures_total{reason="timeout"}` series could never
   increment.
9. **Shutdown retries ignored the shutdown deadline.** The publisher slept through retry
   backoffs and ran database writes after the caller's deadline, delaying shutdown and racing
   the database pool close.
10. **The relay benchmark could not run longer than three seconds.** The test client helper left
    a 3 s socket deadline in place, so `-benchtime 5s` failed with `i/o timeout`. The published
    numbers were only valid for short runs.

## 6. Environment limitations

- All networking was loopback only. No real WAN latency, MTU, packet loss or TLS work is
  represented.
- The load harness, the proxy, the upstream and PostgreSQL share one machine; CPU and memory
  figures are for the proxy process only (`process_cpu_seconds_total`,
  `process_resident_memory_bytes`).
- Loopback connection churn is bounded by the ephemeral port range and `TIME_WAIT`. High
  connection rates can fail with `EADDRNOTAVAIL`; the harness closes abortively to reduce this.
- No `benchstat` was installed, so repeated runs are compared manually with the raw numbers.
- Docker Compose was exercised with the same PostgreSQL 15 image, not against a managed
  database, TLS-terminating proxy or orchestrator.
- No external penetration test, fuzzing campaign or formal security certification was
  performed.
