# Benchmarks and Performance Evidence

All numbers below were measured on the machine described in `docs/audit/baseline.md`. They are
**observations for this environment**, not universal performance targets. Re-run the exact
commands before comparing.

Two suites exist:

1. **Component benchmarks** (`go test -bench`) for isolated hot paths.
2. **`benchmarks/loadtest`** for end-to-end SOCKS5 traffic through a real proxy process with
   PostgreSQL and the analytics pipeline enabled.

## 1. Environment and configuration

| Item | Value |
| --- | --- |
| CPU | Apple M1 Pro, 8 logical cores |
| Memory | 16 GiB |
| OS | macOS 27.0.1, arm64 |
| Go | `go1.27.3` |
| PostgreSQL | 15.19 (container, loopback) |
| Proxy config | defaults except: `max_connections=5000`, `pipeline.buffer_size=50000`, `pipeline.batch_size=200`, `pipeline.flush_interval_ms=1000`, `relay_buffer_kb=32` (default), `log.level=warn` |
| Analytics | enabled (the supported production configuration requires PostgreSQL) |
| Network | loopback only |

The load harness, the upstream server, the proxy and PostgreSQL share this machine. Proxy CPU
and memory are read from the proxy's own Prometheus endpoint (`process_cpu_seconds_total`,
`process_resident_memory_bytes`, `go_goroutines`).

## 2. Component benchmarks

Command:

```bash
go test -run '^$' -bench . -benchmem -count=3 ./internal/proxy ./internal/pipeline ./internal/security
```

Values below are representative runs; variance between repetitions is noted in the text.

| Benchmark | Before | After | Change |
| --- | --- | --- | --- |
| `BenchmarkSOCKS5RelayEcho` (64 KiB round trip) | 87,808 ns/op, 746 MB/s | 86,017 ns/op, 762 MB/s | +2% (within run-to-run variance) |
| `BenchmarkSOCKS5RelayUpload` (1 MiB one-way) | 379,103 ns/op, 2,766 MB/s | 378,831 ns/op, 2,768 MB/s | unchanged at the 32 KiB default |
| `BenchmarkSOCKS5RelayDownload` (1 MiB one-way) | 372,035 ns/op, 2,818 MB/s | 375,705 ns/op, 2,791 MB/s | unchanged (noise) |
| `BenchmarkSOCKS5Handshake/no_auth` | 1,004 ns/op, 4,360 B/op, 13 allocs | 349.5 ns/op, 776 B/op, 13 allocs | **−65% time, −82% bytes** |
| `BenchmarkSOCKS5Handshake/user_password` | 1,059 ns/op, 4,376 B/op, 18 allocs | 417.0 ns/op, 792 B/op, 18 allocs | **−61% time, −82% bytes** |
| `BenchmarkRequestParsing` | 139.4 ns/op, 152 B/op, 7 allocs | 141–145 ns/op, 152 B/op, 7 allocs | unchanged |
| `BenchmarkEventConstruction` | 294 ns/op, 32 B/op, 1 alloc | 295 ns/op, 32 B/op, 1 alloc | unchanged (crypto/rand event ID) |
| `BenchmarkRelayByteAccounting` (32 KiB write) | 40.7 ns/op | 8.2 ns/op | **−80%** |
| `BenchmarkCollectorEnqueue` | 74.6 ns/op, 0 allocs | 74.6 ns/op, 0 allocs | unchanged |
| `BenchmarkEventNormalization` | 5.5 ns/op, 0 allocs | 5.5 ns/op, 0 allocs | unchanged |
| `BenchmarkPublisherBatchPersist` (100 records, fake repo) | 402.7 ns/op, 336 B, 5 allocs | 399 ns/op, 336 B, 5 allocs | unchanged |
| `BenchmarkRateLimiterAllowExisting` | 64.3 ns/op | 64.3 ns/op | unchanged |
| `BenchmarkRateLimiterSaturatedNewClient` (10k tracked, new IP) | **185,929 ns/op** | **293 ns/op** | **≈635× faster** |
| `BenchmarkRateLimiterParallel` | 205 ns/op | 211 ns/op | unchanged (mutex-bound) |
| `BenchmarkIPWhitelistAllowed` | 57.7 ns/op, 8 B, 1 alloc | 57.7 ns/op | unchanged |

### 2.1 Relay buffer size trade-off

`proxy.relay_buffer_kb` selects the per-direction copy buffer (default 32, range 4–1024).
Measured effect for a single 1 MiB stream:

| `relay_buffer_kb` | Upload | Download | 64 KiB echo |
| ---: | ---: | ---: | ---: |
| 32 (default) | 2.77 GB/s | 2.79 GB/s | 762 MB/s |
| 64 | 3.17–3.35 GB/s | 3.50 GB/s | 777 MB/s |

With 16 concurrent 64 KiB sessions the end-to-end difference shrinks to about 4%
(459 → 479 MB/s). Memory cost is real: 500 idle sessions used **92.1 MiB** at 32 KiB and
**125.2 MiB** at 64 KiB (≈64 KiB extra per session). The default stays at 32 KiB; raise it only
for bulk-transfer deployments with memory headroom.

A CPU profile of the echo benchmark showed ~83% of samples in `syscall`/`kevent`, i.e. the
round-trip benchmark is dominated by socket I/O and scheduling rather than Go code. That is why
the per-write accounting fix (5× on the isolated microbenchmark) only moves the end-to-end echo
number by a few percent.

### 2.2 What the microbenchmarks do not measure

- No real network latency, packet loss, MTU or TLS.
- The publisher benchmark uses a fake repository and excludes PostgreSQL.
- The relay benchmarks use one connection and one direction at a time.
- `BenchmarkSOCKS5Handshake` uses an in-memory connection; kernel handshake cost is in the load
  tests.

## 3. End-to-end load harness

### 3.1 Tool

`benchmarks/loadtest` is a Go program that:

- starts a local upstream TCP server (echo, upload/discard, download or idle),
- performs a real SOCKS5 greeting, optional RFC 1929 authentication, and CONNECT,
- relays a configured payload and response size,
- measures connection setup latency percentiles and payload throughput,
- scrapes the proxy's Prometheus endpoint before and after the run,
- compares `socks5_proxy_relay_bytes_total` deltas with the bytes it actually sent/received.

It is not a fake bypass: it goes through the same listener, handshake, policy, relay and
analytics pipeline as production traffic.

Command shape:

```bash
go run ./benchmarks/loadtest \
  -proxy 127.0.0.1:1080 -metrics http://127.0.0.1:9090/metrics \
  -concurrency 32 -duration 20s -warmup 3s \
  -payload 4096 -mode echo -json result.json
```

The harness closes connections with `SO_LINGER=0` to avoid exhausting the loopback ephemeral
port range with `TIME_WAIT`; the relay has already completed when it does so.

### 3.2 Results — final build, default configuration

| Workload | Concurrency | Duration | Connections | Failures | Conn/s | p50 / p90 / p95 / p99 setup (ms) | Payload | Proxy CPU | Proxy RSS | Accounting delta |
| --- | ---: | ---: | ---: | ---: | ---: | --- | --- | ---: | ---: | ---: |
| 4 KiB echo | 32 | 20 s | 181,413 | 0 | 9,069.8 | 2.16 / 2.72 / 3.02 / 3.84 | 35.43 MB/s each direction | 41.8 s | 71.2 MiB | 0 bytes |
| 64 KiB upload | 16 | 15 s | 110,280 | 0 | 7,351.3 | 1.88 / 2.63 / 2.98 / 4.20 | 459.45 MB/s | 27.8 s | 61.0 MiB | 0 bytes |
| 64 KiB download | 16 | 15 s | 97,281 | 0 | 6,483.6 | 1.37 / 2.01 / 2.29 / 3.21 | 405.23 MB/s | 25.9 s | 57.9 MiB | 0 bytes |
| 5 s idle sessions | 500 | 15 s | 1,500 | 0 | 99.2 | 40.0 / 45.1 / 45.8 / 47.1 | — | 0.4 s | 92.1 MiB | 0 bytes |

Goroutine count stayed at 16–18 after the runs, i.e. sessions were fully cleaned up. The idle
run's ~40 ms setup latency is queueing from 500 simultaneous handshakes, not a steady-state
per-connection cost.

### 3.3 Concurrency scaling (4 KiB echo, 10–20 s runs)

| Concurrency | Conn/s | p50 (ms) | p99 (ms) | Failures | Collector drops | Proxy RSS |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 32 | 9,070 | 2.16 | 3.84 | 0 | 0 | 71 MiB |
| 64 | 9,398 | 4.28 | 6.96 | 0 | 0 | 99 MiB |
| 128 | 9,401 | 8.64 | 17.21 | 8 (0.01%) | 0 | 118 MiB |
| 256 | 9,421 | 17.22 | 64.43 | 378 (0.2%) | 41,064 | 147 MiB |

Connection throughput plateaus at ~9.4k/s on this 8-core machine; additional concurrency only
adds queueing latency. At 256 concurrent clients the analytics pipeline falls behind at this
connection rate and the collector queue drops events while forwarding continues. The harness
classified the 378 failures as 28 connect-stage and 350 relay-stage; the proxy admitted
188,792 sessions in the same window and logged no admission refusals, so these were not
allowlist/rate-limit/connection-cap rejections.

### 3.4 Analytics pipeline behavior under database outage

A 40 s echo workload at concurrency 16 was run while PostgreSQL was stopped for 12 s and then
restarted.

| Observation | Result |
| --- | --- |
| Forwarding during the outage | 346,571 connections, **0 failures**, 8,664 conn/s, p99 2.53 ms |
| Database write errors | 20 |
| Collector-stage drops | 20,461 |
| Publisher drops after retries | 1,220 |
| Failed batches | 9 |
| Recovery | `pipeline_events_published_total` resumed immediately after the database returned |
| Byte accounting | delta ≈ 12 KB out of 51.8 MB (0.02%, in-flight sessions at scrape time) |

This confirms the intended contract: the forwarding path does not depend on the database, and
analytics loss during an outage is bounded, counted and visible. It also shows that a 50,000
event queue at ~9k events/s fills in roughly five seconds; size the queue for the outage
duration you want to absorb.

### 3.5 Analytics overhead

A clean "analytics off" comparison is **not possible** in the supported production
configuration: `cmd/proxy` refuses to start without a database, and there is no supported
analytics-disabled mode. The following evidence bounds the overhead instead:

- Per-event pipeline cost: collector enqueue 74.6 ns, normalization 5.5 ns (component
  benchmarks), plus batched database writes.
- At 9.1k connections/s the proxy used 41.8 s of CPU over a 20 s window (~2.1 cores) for the
  whole process, including handshake, relay, metrics, event construction, normalization and
  database batching.
- During the outage test, forwarding continued at 8.7k conn/s with the publisher failing, so
  the forwarding path does not block on database I/O.

No percentage reduction is claimed because no valid baseline exists.

### 3.6 Pipeline configuration comparison

Same workload (4 KiB echo, concurrency 32, 15 s, warm-up 3 s), two pipeline configurations.
These are single runs against a growing table, so treat the difference as indicative, not a
controlled experiment.

| Configuration | Conn/s | p99 setup (ms) | Proxy CPU | Proxy RSS | Drops | Published delta |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Defaults: buffer 10,000, batch 100, flush 5 s, 4 workers | 8,306 | 7.73 | 27.1 s | 56.2 MiB | 0 | 111,604 |
| Tuned: buffer 50,000, batch 200, flush 1 s, 4 workers | 9,025 | 4.80 | 30.3 s | 80.4 MiB | 0 | 143,616 |

Larger batches and a shorter flush interval reduce per-row overhead and the end-of-run backlog;
the cost is more memory and slightly more CPU. Both configurations kept the pipeline lossless at
this rate. Under higher churn (256 clients) the default-sized queue dropped events (see §3.3).

### 3.7 Soak / stability

A 40 s run and multiple 10–20 s runs are **not** a soak test. They show no goroutine growth
(16–18 after each run), no descriptor errors and no memory trend within the window, but they do
not establish long-term stability. A multi-hour soak with a monitored RSS/goroutine trend
remains **NOT RUN**; the harness supports it
(`-duration 4h -concurrency 64 -json soak.json`).

## 4. Performance budgets (proposed, not universal)

These budgets are derived from the measurements above for the deployment profile "single
8-core node, loopback or LAN clients, PostgreSQL on the same host or LAN". They are goals for
regression detection, not vendor claims.

| Area | Target workload | Observed baseline | Budget | Allowed regression | Failure threshold | Method |
| --- | --- | --- | --- | --- | --- | --- |
| Connection setup (p99) | 4 KiB echo, 32 clients | 3.84 ms | ≤ 10 ms | +20% | > 25 ms for 3 runs | `loadtest -mode echo -concurrency 32` |
| Connection setup (p99) | 4 KiB echo, 128 clients | 17.2 ms | ≤ 30 ms | +20% | > 50 ms | same, `-concurrency 128` |
| Connection throughput | 4 KiB echo, 32 clients | 9,070/s | ≥ 7,500/s | −15% | < 6,000/s | same |
| Bulk upload | 64 KiB, 16 clients | 459 MB/s | ≥ 350 MB/s | −20% | < 300 MB/s | `-mode upload -payload 65536` |
| Bulk download | 64 KiB, 16 clients | 405 MB/s | ≥ 300 MB/s | −20% | < 250 MB/s | `-mode download` |
| Relay overhead per 32 KiB write | component | 8.2 ns | ≤ 20 ns | +50% | > 50 ns | `BenchmarkRelayByteAccounting` |
| Handshake parse | component | 350 ns, 776 B | ≤ 700 ns, ≤ 1 KiB | +100% | > 1.5 µs | `BenchmarkSOCKS5Handshake` |
| Memory per idle session | 500 idle sessions | ≈190 KiB (RSS incl. runtime) | ≤ 256 KiB | +25% | > 512 KiB | `-mode idle -concurrency 500` |
| Goroutines after run | any | 16–18 | ≤ 2× baseline | — | growth across repeats | `/metrics` `go_goroutines` |
| Analytics loss | healthy database | 0 drops | 0 | — | any drop at ≤ 5k events/s | `pipeline_events_dropped_total` |
| Shutdown (no active sessions) | idle | 0.3 ms | ≤ 2 s | — | > 5 s | SIGTERM timing |
| Shutdown (active sessions) | grace 2 s | 2.05 s | grace + 2 s | — | > grace + 5 s | SIGTERM with load |

Hard invariants (not performance goals): policy denial must happen before dial, byte counters
must match payload, and no authentication or allowlist check may be skipped.

## 5. Reproducibility checklist

1. Use the same commit and Go toolchain.
2. Start PostgreSQL 15 and the proxy with the configuration in §1.
3. Run `go test -run '^$' -bench . -benchmem -count=3 ./internal/proxy ./internal/pipeline ./internal/security`.
4. Run the load harness with the exact flags in §3.2; discard the first warm-up window.
5. Repeat each load scenario at least three times and compare the median, not a single run.
6. Record the commit, flags, proxy configuration, CPU count and database version with the
   result (the harness writes them into the JSON report).

Known confounders: shared CPU with the harness and PostgreSQL, loopback-only networking,
`TIME_WAIT`/ephemeral port limits, macOS scheduler noise, and the analytics backlog at the end
of a run (Prometheus deltas may exclude in-flight events).
