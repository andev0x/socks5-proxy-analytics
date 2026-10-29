# Verification Report

> **Historical document.** This describes an earlier iteration and may not match the current
> implementation. The current evidence is in [audit/baseline.md](audit/baseline.md),
> [audit/correctness.md](audit/correctness.md), [audit/security.md](audit/security.md),
> [benchmarks.md](benchmarks.md), and the release status is in
> [release-readiness.md](release-readiness.md).


## Environment

- OS: macOS (darwin/arm64)
- Go: module declares `go 1.26.0`
- PostgreSQL: local instance reachable on `/tmp` socket, user `andev0x`
- Docker: not executed in this session

## Commands executed

| Command | Result |
|---|---|
| `gofmt -l .` | PASS (no output) |
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `go test ./...` | PASS |
| `go test -race ./...` | PASS |
| `golangci-lint run ./...` | PASS (0 issues) |
| `govulncheck ./...` | PASS (no vulnerabilities in project code) |
| `gosec ./...` | PASS (0 issues) |
| `go test -run '^$' -bench . -benchmem -count=1 ./internal/proxy ./internal/pipeline` | PASS |
| `TEST_DB_HOST=localhost TEST_DB_PORT=5432 TEST_DB_USER=andev0x TEST_DB_PASSWORD= TEST_DB_NAME=socks5_test TEST_DB_SSLMODE=disable go test -race ./...` | PASS (storage and proxy E2E executed) |

## Integration coverage

- PostgreSQL repository integration: executed and passed.
- Proxy-to-PostgreSQL E2E: executed and passed.
- Docker Compose runtime: NOT RUN (daemon unavailable in this session).
- Policy network-boundary E2E against a controlled upstream: covered by
  `TestPolicyEnforcementDeniesBeforeDial`; NOT RUN as a separate live deployment test.

## Benchmark results

- `BenchmarkSOCKS5RelayEcho`: 84,236 ns/op, 778.00 MB/s, 2 B/op, 0 allocs/op
- `BenchmarkCollectorEnqueue`: 74.31 ns/op, 0 B/op, 0 allocs/op
- `BenchmarkEventNormalization`: 5.456 ns/op, 0 B/op, 0 allocs/op
- `BenchmarkPublisherBatchPersist`: 394.4 ns/op, 100 records/op, 336 B/op, 5 allocs/op

These are local loopback microbenchmarks and are not production capacity claims.
