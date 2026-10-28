# Phase 1 Baseline Benchmarks

These numbers are a **local baseline**, not performance targets or production claims. They
exist so future changes can be compared on the same machine and method.

## Environment

| Item | Value |
| --- | --- |
| CPU | Apple M1 Pro (8 cores) |
| OS | macOS 27.0.1 (build 26A434) |
| Go | `go1.27.3 darwin/arm64` |
| Module Go directive | `go 1.26.0` |
| Date | 2026-10-29 |
| Network | loopback only (`127.0.0.1`) |

## Method

```bash
go test -run '^$' -bench . -benchmem -count=3 ./internal/proxy ./internal/pipeline
```

- Each benchmark runs three times; all three results are reported below.
- `-benchmem` records allocations. No CPU profiling or external load generator was used.
- Benchmarks are sequential unless stated; `GOMAXPROCS` is the Go default for 8 cores.

### `BenchmarkSOCKS5RelayEcho` (proxy)

One persistent SOCKS5 CONNECT session to a local echo upstream. The client writes a 64 KiB
payload request and reads the same 64 KiB response for each iteration. The one-time TCP
connect, SOCKS5 handshake and authentication are **excluded** from the timing; relay copies,
per-write byte accounting and connection teardown at benchmark exit are included. `MB/s` is
payload throughput reported by `b.SetBytes(65536)`.

### `BenchmarkCollectorEnqueue` (pipeline)

Measures the non-blocking collector enqueue path with a bounded 1024-event channel. The
benchmark loop drains one event when the channel is full so the accepted path is measured
without relying on goroutine scheduling. Production saturation behavior (drop + metric) is
covered by `TestCollectorDropsWhenQueueFull`.

### `BenchmarkEventNormalization` (pipeline)

Measures conversion of one `ConnectionEvent` into the persisted `TrafficLog` struct. No
database or channel operations are included.

### `BenchmarkPublisherBatchPersist` (pipeline)

Calls the publisher's batch flush path with 100 records and an in-memory fake repository
that discards writes. This measures publisher bookkeeping (metrics, histogram observations,
logging setup) **without PostgreSQL**; it is not database throughput. Real database
throughput is environment-dependent and was not benchmarked here.

## Results

### Proxy relay

| Run | ns/op | MB/s | B/op | allocs/op |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 87,163 | 751.88 | 0 | 0 |
| 2 | 91,316 | 717.68 | 0 | 0 |
| 3 | 96,615 | 678.32 | 0 | 0 |

Approximate range: **678–752 MB/s** for 64 KiB request/response on loopback (~10.3–11.5k
round trips/second). The three runs vary by roughly 11%, illustrating normal run-to-run
variance on the same machine; compare using the same command and environment.

### Pipeline

| Benchmark | Run 1 | Run 2 | Run 3 | Allocations |
| --- | ---: | ---: | ---: | --- |
| `BenchmarkCollectorEnqueue` | 72.29 ns/op | 72.39 ns/op | 72.27 ns/op | 0 B/op, 0 allocs/op |
| `BenchmarkEventNormalization` | 4.668 ns/op | 4.673 ns/op | 4.684 ns/op | 0 B/op, 0 allocs/op |
| `BenchmarkPublisherBatchPersist` | 398.8 ns/op | 396.9 ns/op | 396.8 ns/op | 336 B/op, 5 allocs/op |

`BenchmarkPublisherBatchPersist` reports `100.0 records/op`; the ns/op figure is per
100-record flush against the fake repository.

## Interpretation and caveats

- Loopback numbers exclude real network latency and TLS/HTTP work by clients and upstreams.
- The proxy benchmark uses one connection and one direction at a time; concurrent-connection
  scaling was not measured.
- The publisher benchmark deliberately excludes PostgreSQL; do not cite it as storage
  throughput.
- Microbenchmarks can be optimized or perturbed by the compiler and machine state. Rerun
  the exact command above before comparing results.
- No claims are made about maximum concurrent connections, tail latency, memory ceilings or
  production capacity. Those require a documented load test with targets.
