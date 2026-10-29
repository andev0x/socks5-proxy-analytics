# Operations Guide

This guide covers deployment, configuration, monitoring, troubleshooting, recovery and
capacity planning for the two processes in this repository:

- `cmd/proxy` — SOCKS5 listener, Prometheus endpoint, analytics pipeline, database writes.
- `cmd/api` — read-only analytics REST API and database-backed readiness.

Both share one PostgreSQL database.

## 1. Prerequisites

| Component | Tested version | Notes |
| --- | --- | --- |
| Go (source builds) | 1.27.3 | `go.mod` declares 1.26.10; both build |
| PostgreSQL | 15.19 | 15+ expected; newer versions not verified |
| Docker / Compose | 29.2.1 / Compose v2 | for the local stack |

Required database settings: `DB_HOST`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`. The process
refuses to start without them.

## 2. Configuration

Precedence, highest first:

1. Environment variables.
2. The YAML file selected by `CONFIG_FILE`, or `configs/config.yml` under `CONFIG_PATH`
   (default `./configs`).
3. Built-in defaults.
4. `.env` in the working directory (loaded by `godotenv`; it does **not** override variables
   already set in the environment).

`configs/config.example.yml` and `.env.example` are development starting points. Never commit
real credentials; `configs/config.yml` and `.env` are gitignored.

### 2.1 Proxy

| Key | Env | Default | Meaning |
| --- | --- | --- | --- |
| `proxy.address` | `PROXY_ADDRESS` | `127.0.0.1` | Listener bind address (IP or `localhost`) |
| `proxy.port` | `PROXY_PORT` | `1080` | Listener port |
| `proxy.max_connections` | `PROXY_MAX_CONNECTIONS` | `10000` | Global admitted-session cap |
| `proxy.dial_timeout_ms` | `PROXY_DIAL_TIMEOUT_MS` | `10000` | Upstream dial and policy DNS timeout |
| `proxy.handshake_timeout_ms` | `PROXY_HANDSHAKE_TIMEOUT_MS` | `10000` | Full handshake deadline |
| `proxy.shutdown_grace_ms` | `PROXY_SHUTDOWN_GRACE_MS` | `5000` | Grace for active sessions |
| `proxy.idle_timeout_ms` | `PROXY_IDLE_TIMEOUT_MS` | `0` | Relay idle timeout; `0` disables it |
| `proxy.relay_buffer_kb` | `PROXY_RELAY_BUFFER_KB` | `32` | Per-direction copy buffer (4–1024) |
| `proxy.auth.enabled` | `PROXY_AUTH_ENABLED` | `false` | Require RFC 1929 auth |
| `proxy.auth.username` / `password` | `PROXY_AUTH_USERNAME` / `PROXY_AUTH_PASSWORD` | empty | Legacy single credential |
| `proxy.workloads[]` | YAML only | empty | Workload identities (`id`, `username`, `password`) |
| `proxy.ip_whitelist` | `PROXY_IP_WHITELIST` | empty | Comma-separated IPs/CIDRs; empty allows all |

When `proxy.workloads` is non-empty, `proxy.auth.enabled` must be true and the workload
credentials replace the legacy username/password. Startup fails otherwise.

### 2.2 Pipeline

| Key | Env | Default | Meaning |
| --- | --- | --- | --- |
| `pipeline.workers` | `PIPELINE_WORKERS` | `4` | Normalizer workers |
| `pipeline.buffer_size` | `PIPELINE_BUFFER_SIZE` | `10000` | Capacity of each bounded channel |
| `pipeline.batch_size` | `PIPELINE_BATCH_SIZE` | `100` | Records per insert batch |
| `pipeline.flush_interval_ms` | `PIPELINE_FLUSH_INTERVAL_MS` | `5000` | Max batch age |
| `pipeline.max_retries` | `PIPELINE_MAX_RETRIES` | `3` | Retries after the first attempt |
| `pipeline.retry_backoff_ms` | `PIPELINE_RETRY_BACKOFF_MS` | `200` | Exponential base, capped at 5 s |
| `pipeline.flush_timeout_ms` | `PIPELINE_FLUSH_TIMEOUT_MS` | `5000` | Per-write deadline |

### 2.3 Database, API, metrics, logging, shutdown

| Key | Env | Default | Meaning |
| --- | --- | --- | --- |
| `database.host/port/user/password/database` | `DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME` | required | PostgreSQL connection |
| `database.sslmode` | `DB_SSLMODE` | `disable` | `disable`…`verify-full` |
| `database.connect_timeout_ms` | `DB_CONNECT_TIMEOUT_MS` | `5000` | Connect, ping and migration-lock timeout |
| `database.max_open_conns` / `max_idle_conns` | `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` | `20` / `10` | Connection pool |
| `api.address/port` | `API_ADDRESS/API_PORT` | `127.0.0.1:8080` | REST API |
| `api.read_timeout_ms` / `write_timeout_ms` / `idle_timeout_ms` | `API_*` | `10000` / `10000` / `60000` | HTTP timeouts |
| `metrics.enabled` | `METRICS_ENABLED` | `true` | Serve `/metrics` and `/healthz` |
| `metrics.address/port` | `METRICS_ADDRESS` / `METRICS_PORT` | `127.0.0.1:9090` | Prometheus endpoint |
| `logging.level` / `format` | `LOG_LEVEL` / `LOG_FORMAT` | `info` / `json` | `debug|info|warn|error`, `json|text` |
| `rate_limit.enabled` | `RATE_LIMIT_ENABLED` | `false` | Per-source-IP new-connection limit |
| `rate_limit.requests_per_second` | `RATE_LIMIT_RPS` | `100` | Token bucket refill rate |
| `rate_limit.max_tracked_clients` | `RATE_LIMIT_MAX_TRACKED_CLIENTS` | `100000` | Bounded limiter state |
| `shutdown.timeout_ms` | `SHUTDOWN_TIMEOUT_MS` | `15000` | Whole-process shutdown budget |

`shutdown.timeout_ms` must be greater than `proxy.shutdown_grace_ms`; startup fails otherwise.

### 2.4 Policy

```yaml
policy:
  enabled: true
  id: "egress-v1"
  version: "1"
  default_action: "deny"          # only "deny" is accepted
  restricted_cidrs: []            # defaults: loopback, link-local, 0.0.0.0/8, ::/128
  rules:
    - id: "allow-api"
      workload: "agent-1"         # empty or "*" matches any workload
      action: "allow"
      host: "api.example.com"     # exact hostname
      port: 443
    - id: "allow-cidr"
      workload: "agent-1"
      action: "allow"
      ip: "203.0.113.0/24"
      port_start: 443
      port_end: 443
```

Rules are evaluated per resolved candidate address. The proxy dials the exact address that was
allowed. A deny rule always wins over an allow rule; higher `priority` wins; ties use
declaration order.

For SSRF containment, add private and metadata ranges explicitly:

```yaml
restricted_cidrs:
  - 10.0.0.0/8
  - 172.16.0.0/12
  - 192.168.0.0/16
  - 169.254.0.0/16
  - 127.0.0.0/8
  - fc00::/7
  - fe80::/10
  - ::1/128
```

## 3. Deployment

### 3.1 Local / single host (Docker Compose)

```bash
docker compose -f deployments/docker-compose.yml up -d --build
```

This starts PostgreSQL, the proxy (`127.0.0.1:1080`, metrics `127.0.0.1:9090`) and the API
(`127.0.0.1:8080`). All host ports bind to loopback. The default database credentials are
development-only; override `DB_USER`, `DB_PASSWORD`, `DB_NAME` for anything shared.

The Compose stack sets `stop_grace_period: 30s` so Docker does not `SIGKILL` the proxy before
its default 15 s shutdown budget completes.

### 3.2 Production requirements

- Enable authentication (`proxy.auth.enabled`) and/or an IP allowlist before exposing the
  listener. Without authentication and bound to a non-loopback address, the proxy is an open
  proxy; it logs a warning but still starts.
- Put the analytics API and metrics behind an authenticated reverse proxy and TLS. Neither has
  application-level authentication.
- Use `DB_SSLMODE=verify-full` with a trusted CA for any database that is not on the same host.
- Set a finite `proxy.idle_timeout_ms` when clients are untrusted.
- Set `proxy.max_connections` to a value the host can actually serve (see §9).
- Persist PostgreSQL data on a volume with backups; `traffic_logs` grows without bound unless
  you add retention (§7).

### 3.3 Container behavior

- Images run as a non-root user (`appuser`, uid 1000).
- The proxy image health check probes `http://127.0.0.1:9090/healthz`. **If
  `METRICS_ENABLED=false`, the container is reported unhealthy** even though the proxy is
  working. Keep metrics enabled in containers, or override the health check.
- The API image health check probes `/health`, which requires the database. A database outage
  marks the API unhealthy; Docker does not restart unhealthy containers by itself.
- Signal handling: both processes handle `SIGTERM`/`SIGINT` and shut down gracefully.

## 4. Health checks: liveness vs readiness

| Endpoint | Process | Checks | Use for |
| --- | --- | --- | --- |
| `GET /healthz` (proxy metrics port) | proxy | process is serving HTTP | container liveness |
| `GET /livez` | API | process is serving HTTP | container liveness |
| `GET /health` and `GET /readyz` | API | PostgreSQL ping (2 s timeout) | readiness / load-balancer membership |
| `GET /metrics` | proxy | Prometheus exposition | scraping |

A database outage does not make the proxy unready: forwarding is independent of the database by
design. Readiness for the API means "can answer queries".

## 5. Monitoring

Scrape `http://<proxy>:9090/metrics` every 15–30 s.

### 5.1 Key signals

| Signal | Why it matters |
| --- | --- |
| `socks5_proxy_connections_accepted_total` | admitted sessions |
| `socks5_proxy_connections_rejected_total{reason}` | allowlist, rate limit, connection cap, shutdown |
| `socks5_proxy_active_connections` | current concurrency vs `max_connections` |
| `socks5_proxy_handshake_failures_total{reason}` | malformed/timeout/auth failures |
| `socks5_proxy_dial_errors_total{reason}` | upstream/DNS failures |
| `socks5_proxy_dial_latency_seconds` | upstream reachability |
| `socks5_proxy_connection_duration_seconds` | session mix |
| `socks5_proxy_relay_bytes_total{direction}` | payload volume |
| `socks5_proxy_relay_errors_total` | post-connect relay failures |
| `socks5_proxy_policy_decisions_total{result}` | allow/deny volume |
| `pipeline_events_collected_total` | events entering the pipeline |
| `pipeline_events_dropped_total{stage}` | **loss** at the collector (`normalizer` is reserved and stays 0; the normalizer applies backpressure) |
| `pipeline_events_publish_dropped_total` | **loss** after retries / shutdown abort |
| `pipeline_publish_failures_total` | failed batches |
| `pipeline_publish_retries_total` | database instability |
| `pipeline_publish_duration_seconds` | database write latency |
| `pipeline_queue_events{stage}` | backlog |
| `db_errors_total` | database write errors |
| `process_shutdowns_total{result}` | clean vs forced shutdown |
| `go_goroutines`, `process_resident_memory_bytes`, `process_cpu_seconds_total` | resource trends |

All labels are bounded enums. There are no hostname, IP or event-ID labels.

### 5.2 Alert suggestions

Thresholds below are anchored to the measurements in `docs/benchmarks.md`; adjust for your
hardware and workload.

| Alert | Expression (starting point) | Rationale |
| --- | --- | --- |
| Analytics loss | `rate(pipeline_events_dropped_total[5m]) > 0` for 5 m | Any sustained drop means the pipeline cannot keep up or the database is down. |
| Publish loss | `increase(pipeline_events_publish_dropped_total[5m]) > 0` | Events were permanently discarded. |
| Database unhealthy | `rate(db_errors_total[5m]) > 0` for 5 m | Writes are failing; forwarding still works. |
| Connection cap reached | `increase(socks5_proxy_connections_rejected_total{reason="max_connections"}[5m]) > 0` | Clients are being refused by the global cap. |
| Rate-limit storm | `rate(socks5_proxy_connections_rejected_total{reason="rate_limited"}[5m])` high | Possible abuse or an undersized limit. |
| Auth failures | `rate(socks5_proxy_auth_failures_total[5m])` spike | Credential stuffing or a misconfigured client. |
| Handshake timeouts | `rate(socks5_proxy_handshake_failures_total{reason="timeout"}[5m])` spike | Slow or malicious clients, or an undersized timeout. |
| Upstream failures | `rate(socks5_proxy_dial_errors_total[5m])` spike | Upstream or DNS problem. |
| Backlog | `pipeline_queue_events{stage="collector"} > 0.8 * buffer_size` | Queue about to overflow. |
| Tail latency | `histogram_quantile(0.99, rate(socks5_proxy_dial_latency_seconds_bucket[5m]))` | Upstream degradation. |
| Forced shutdown | `increase(process_shutdowns_total{result="timeout"}[1h]) > 0` | Sessions or pipeline did not drain in time. |
| Memory growth | `deriv(process_resident_memory_bytes[1h]) > 0` sustained | Possible leak. |

### 5.3 Dashboards

A minimal dashboard: accepted/rejected rate, active connections, dial-error rate, relay
throughput, queue depth by stage, drop rate, publish latency, goroutines and RSS.

## 6. Troubleshooting

| Symptom | Likely cause | Action |
| --- | --- | --- |
| Startup fails with `critical: DB_* not set` | missing database configuration | set `DB_HOST`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` |
| Startup fails with `proxy.workloads requires proxy.auth.enabled` | workloads configured without auth | enable auth or remove workloads |
| Startup fails with `policy.enabled requires ...` | policy without auth/workloads | enable auth and configure workloads |
| Startup fails with `timed out waiting for the migration lock` | another process is migrating, or a large migration | raise `DB_CONNECT_TIMEOUT_MS`, check for a stuck migration |
| `analytics queue full` warnings | pipeline slower than event rate or database down | inspect `pipeline_queue_events`, `db_errors_total`; raise buffer/batch or fix the database |
| Forwarding works, API `/health` returns 503 | database unavailable | restore PostgreSQL; the proxy keeps forwarding |
| High p99 setup latency | concurrency above the host's capacity | lower client concurrency or scale out (see §9) |
| `connection limit` rejections | `max_connections` reached | raise the cap only after checking memory/CPU budgets |
| `can't assign requested address` in load tests | ephemeral port exhaustion on loopback | reduce connection rate or run the generator on another host |
| Container unhealthy but proxy logs look fine | metrics disabled, health check cannot reach `/healthz` | enable metrics or override the health check |

## 7. Backup, restore and retention

Backup:

```bash
pg_dump -Fc -U socks5 -d socks5_analytics -f socks5_analytics.dump
```

Restore:

```bash
pg_restore -U socks5 -d socks5_analytics --clean --if-exists socks5_analytics.dump
```

Retention (not automated by the application):

```sql
-- Example: delete analytics older than 30 days, in bounded batches.
DELETE FROM traffic_logs
WHERE id IN (
  SELECT id FROM traffic_logs
  WHERE timestamp < now() - interval '30 days'
  LIMIT 10000
);
```

Run retention during low traffic and repeat until no rows remain. The table has indexes on
`timestamp`, `outcome`, `client_ip`, `requested_host`, `destination_ip` and `workload_id`;
`event_id` is unique.

Schema changes are applied with `AutoMigrate` at startup, serialized by a PostgreSQL advisory
lock so the proxy and API can start together. `AutoMigrate` is additive; it does not drop
columns. Take a backup before upgrading across major schema changes and verify `/health` plus a
test connection afterward.

## 8. Graceful shutdown and upgrades

On `SIGTERM`:

1. The listener closes; new connections are refused and counted as `shutting_down`.
2. Active sessions get `proxy.shutdown_grace_ms`; then they are force-closed and produce
   `server_shutdown` events.
3. The analytics pipeline drains, flushes pending batches (bounded by the remaining
   `shutdown.timeout_ms`) and aborts retries if the deadline is hit. Aborted records are counted
   as publish drops.
4. The metrics server stops and the database pool closes.

Measured: 0.3 ms with no active sessions; 2.05 s with a 2 s grace and 20 active sessions.

Upgrade procedure:

1. Back up the database.
2. Deploy the new binary/image.
3. Start it; the advisory-locked migration runs.
4. Verify `/health` (API) and a real proxied request.
5. Watch `pipeline_events_dropped_total` and `db_errors_total` for 10 minutes.
6. Roll back by redeploying the previous image if error rates rise; `AutoMigrate` is additive,
   so an older binary can normally read the newer schema.

## 9. Capacity planning

Measured on an 8-core Apple M1 Pro with PostgreSQL on the same host (see
`docs/benchmarks.md`):

| Metric | Observed |
| --- | --- |
| 4 KiB echo connection rate | ~9,400/s, plateau at 64–256 concurrent clients |
| 64 KiB upload | ~459 MB/s at 16 concurrent sessions |
| 64 KiB download | ~405 MB/s at 16 concurrent sessions |
| Proxy CPU at ~9.1k conn/s | ~2.1 cores |
| Proxy RSS at ~9.1k conn/s, 32 clients | ~71 MiB |
| Memory per idle session (32 KiB buffer, incl. runtime) | ≈190 KiB |
| Analytics throughput (batch 200, flush 1 s, 4 workers, local DB) | ~7–9k events/s; backlog at 9.4k/s, drops at 256 clients |

Rules of thumb:

- Budget ~200 KiB of proxy RSS per active session plus Go runtime overhead; 10,000 sessions
  needs roughly 2 GiB.
- Keep `max_connections` below the point where p99 setup latency exceeds your SLO. On this
  hardware, 128 concurrent churn clients already produced 17 ms p99.
- Size `pipeline.buffer_size` for the outage you want to absorb:
  `buffer ≈ event_rate × outage_seconds`. At 9k events/s, 50,000 events absorb ~5.5 s.
- The database is the analytics bottleneck. Batch inserts of 200 rows sustained ~9k events/s on
  this host; remote databases will be slower.
- Connection churn is expensive; long-lived sessions are far cheaper per byte.

## 10. Security boundaries

See `docs/audit/security.md` for the full findings and residual risks. Operational summary:

- The API and metrics endpoint have **no application authentication**. Bind them to loopback or
  protect them with a reverse proxy.
- Enable auth and/or an allowlist before exposing the SOCKS5 listener.
- Policy is default-deny, but private/metadata ranges are not restricted unless configured.
- Analytics are best-effort; they are not a durable audit log.
- Client IPs, hostnames and destinations are persisted and returned by the API; apply access
  control and retention appropriate to that data.
