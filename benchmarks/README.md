# Load-test harness

`benchmarks/loadtest` is a deterministic SOCKS5 workload generator. It drives a **running**
proxy with real handshakes and real TCP relay traffic, then reports throughput, setup-latency
percentiles, failures, proxy CPU/memory and relay byte-accounting drift.

It is not a microbenchmark and it is not a fake bypass: it exercises the same listener,
authentication, policy, relay and analytics pipeline as production traffic.

## Build

```bash
go build -o bin/loadtest ./benchmarks/loadtest
```

## Run

```bash
# 1. Start PostgreSQL and the proxy (analytics enabled, as in production).
docker compose -f deployments/docker-compose.yml up -d --build

# 2. Drive 32 concurrent clients for 20 s after a 3 s warm-up.
./bin/loadtest \
  -proxy 127.0.0.1:1080 \
  -metrics http://127.0.0.1:9090/metrics \
  -concurrency 32 -duration 20s -warmup 3s \
  -payload 4096 -mode echo \
  -json loadtest-result.json
```

Or use `make loadtest`.

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-proxy` | `127.0.0.1:1080` | SOCKS5 proxy address |
| `-metrics` | `http://127.0.0.1:9090/metrics` | Proxy Prometheus endpoint; empty skips metrics |
| `-username`, `-password` | empty | RFC 1929 credentials; empty means no-auth |
| `-concurrency` | `32` | Concurrent client goroutines |
| `-duration` | `30s` | Measurement window |
| `-warmup` | `5s` | Warm-up window, excluded from results |
| `-rate` | `0` | Global connection attempts/second; `0` = as fast as possible |
| `-payload` | `4096` | Bytes uploaded per session |
| `-response` | `4096` | Bytes read per session (echo mirrors `-payload`; upload/idle read 0) |
| `-hold` | `0` | Extra time to keep each session open |
| `-mode` | `echo` | `echo`, `upload` (discard), `download`, `idle` |
| `-domain` | empty | Request this domain instead of an IP literal (exercises DNS) |
| `-dial-timeout` | `5s` | Per-connection dial timeout |
| `-io-timeout` | `30s` | Per-connection I/O timeout |
| `-json` | empty | Write the full result as JSON |

## What the report contains

- `connections_per_second` (attempts) and `successful_connections_per_second` (keep these
  distinct).
- Setup latency `p50/p90/p95/p99/max` in milliseconds.
- Payload bytes/sent and bytes/received, plus the upstream's own counters.
- Proxy `process_cpu_seconds_total`, `process_resident_memory_bytes`, `go_goroutines`.
- `pipeline_events_dropped_total`, `pipeline_events_published_total`,
  `pipeline_publish_failures_total` deltas.
- `accounting`: expected vs observed `socks5_proxy_relay_bytes_total` deltas.

## Methodology notes

- Warm-up traffic is discarded so page faults and connection-pool warm-up do not contaminate
  the measurement.
- The harness and the proxy share a machine; the harness CPU is not measured. Do not attribute
  the full machine's CPU to the proxy.
- Loopback connection churn is limited by the ephemeral port range and `TIME_WAIT`. The harness
  closes with `SO_LINGER=0` to reduce this, but very high rates can still fail with
  `EADDRNOTAVAIL`; the harness backs off 10 ms after connect-stage failures.
- Analytics cannot be disabled in the supported production configuration, so every run includes
  PostgreSQL writes. Compare identical configurations, not "analytics on vs off".
- Prometheus deltas are scraped immediately after the window; events still in flight are
  excluded. Use `pipeline_queue_events` and `pipeline_events_published_total` after the run to
  confirm the pipeline drained.
- A single run is not a stability proof. Run the same scenario at least three times and compare
  medians; for soak tests use a multi-hour `-duration` and watch `go_goroutines`,
  `process_resident_memory_bytes` and `pipeline_queue_events`.

## Scenarios used in `docs/benchmarks.md`

```bash
# Connection churn, small payload
loadtest -concurrency 32 -duration 20s -warmup 3s -payload 4096 -mode echo

# Concurrency scaling
loadtest -concurrency 64  -duration 10s -warmup 2s -payload 4096 -mode echo
loadtest -concurrency 128 -duration 10s -warmup 2s -payload 4096 -mode echo
loadtest -concurrency 256 -duration 20s -warmup 3s -payload 4096 -mode echo

# Bulk one-way transfer
loadtest -concurrency 16 -duration 15s -warmup 2s -payload 65536 -mode upload
loadtest -concurrency 16 -duration 15s -warmup 2s -payload 1024 -response 65536 -mode download

# Many long-lived idle sessions (memory per session)
loadtest -concurrency 500 -duration 15s -warmup 1s -payload 0 -response 0 -mode idle -hold 5s
```

## Database outage test

Run a long load and stop PostgreSQL mid-run:

```bash
loadtest -concurrency 16 -duration 40s -warmup 3s -payload 4096 -mode echo &
sleep 10
docker stop <postgres-container>
sleep 12
docker start <postgres-container>
wait
```

Forwarding must continue with zero connection failures; `db_errors_total`,
`pipeline_events_dropped_total` and `pipeline_events_publish_dropped_total` must increase; and
`pipeline_events_published_total` must resume after recovery.
