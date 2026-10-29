# SOCKS5 Proxy Analytics

A Go SOCKS5 proxy with an asynchronous traffic-analytics pipeline, PostgreSQL storage, a
Prometheus metrics endpoint and a small analytics API.

**Phase 2 status:** controlled egress (workload identity + deterministic policy) is implemented
and tested on top of the Phase 1 observability foundation. It remains **not production-ready**.
See [docs/phase-2-design.md](docs/phase-2-design.md) and
[docs/phase-2-closure-report.md](docs/phase-2-closure-report.md).

- Architecture baseline: [docs/socks5-proxy-analytics-architecture.md](docs/socks5-proxy-analytics-architecture.md)
- Phase 1 plan, decisions and evidence: [docs/phase-1-implementation-plan.md](docs/phase-1-implementation-plan.md)
- Benchmarks and methodology: [docs/benchmarks.md](docs/benchmarks.md)

## What is implemented

| Area | Phase 1 behavior |
| --- | --- |
| SOCKS5 | RFC 1928 **CONNECT over TCP**; IPv4, IPv6 and domain address types |
| Authentication | Optional RFC 1929 username/password, enforced in the handshake |
| Client rules | Exact-IP/CIDR allowlist and per-source-IP new-connection rate limit |
| Limits | `max_connections`, handshake timeout and upstream dial timeout are enforced |
| Relay accounting | Explicit client→upstream and upstream→client byte counters |
| Attribution | Client address, requested hostname and resolved destination IP stored separately |
| Outcomes | Every admitted session produces one event, including dial, auth and handshake failure paths |
| Metrics | Prometheus registry served on a real endpoint and updated by runtime paths |
| Pipeline | Bounded best-effort queue, batching, bounded retries, visible loss |
| Shutdown | Stop accept, bounded session grace, drain producers, flush final batch, close DB |
| API | Destination/client aggregate queries, traffic logs, DB-backed health |

BIND and UDP ASSOCIATE are intentionally **not supported** and are rejected with a SOCKS5
"command not supported" reply.

## Architecture map

```
cmd/proxy                 proxy process: listener, metrics server, pipeline, DB
cmd/api                   analytics API process: Gin HTTP server + repository
internal/proxy            SOCKS5 handshake, auth, dial, relay, limits, lifecycle
internal/security         authenticator, IP/CIDR allowlist, bounded rate limiter
internal/pipeline         collector -> normalizer -> batch publisher -> storage
internal/models           persisted traffic schema and aggregate result types
internal/storage          PostgreSQL/GORM repository and schema migration
internal/metrics          dedicated Prometheus registry and HTTP handler
internal/handlers         analytics API handlers and input validation
internal/config           validated configuration and environment overrides
```

The proxy and API are separate processes. Both require PostgreSQL at startup. The proxy
never performs database I/O on the forwarding path.

## Event semantics

Every admitted SOCKS5 session emits exactly one analytics event. Pre-handshake rejects
(connection limit, rate limit, client allowlist) are counted in metrics/logs but have no
destination and therefore do not fabricate an event.

| Field | Meaning |
| --- | --- |
| `event_id` | Random correlation/deduplication identity (unique index) |
| `client_ip`, `client_port` | Peer of the **SOCKS5 TCP connection** (never the upstream) |
| `requested_host` | Hostname exactly as requested; empty for IP literals |
| `destination_ip` | IP actually dialed, taken from the established upstream socket |
| `destination_port` | Requested destination port |
| `protocol` | `tcp` |
| `outcome` | `connected`, `dial_failed`, `auth_failed`, `handshake_failed` |
| `termination_reason` | `client_close`, `upstream_close`, `relay_error`, `server_shutdown`, `dial_error`, `auth_failed`, `no_acceptable_auth`, `malformed_request`, `unsupported_command`, `unsupported_address_type` |
| `timestamp` | Session start (accept time) |
| `duration_ms` | Accept-to-finish duration in milliseconds |
| `dial_latency_ms` | Upstream TCP connect latency in milliseconds (may round to 0 locally) |
| `bytes_client_to_upstream` | Application bytes read from client and written upstream |
| `bytes_upstream_to_client` | Application bytes read downstream and written to client |
| `schema_version` | Event schema version (`1`) |

**Byte rule:** counters include only application payload relayed after the SOCKS5 success
reply. Handshake, authentication and request bytes are excluded. Both directions are tested
with controlled payloads.

## Delivery guarantee (best-effort telemetry)

Connection analytics are **best-effort telemetry, not a durable audit log**:

- the queue is bounded by `PIPELINE_BUFFER_SIZE`;
- enqueue is non-blocking; when full, the event is dropped and counted in
  `pipeline_events_dropped_total{stage="collector"}`;
- a batch is written atomically with a unique `event_id`; retries are bounded by
  `PIPELINE_MAX_RETRIES` with exponential backoff;
- after retries are exhausted the batch is dropped and counted in
  `pipeline_publish_failures_total` and `pipeline_events_publish_dropped_total`; it is never
  reported as published;
- shutdown drains producers and flushes the final batch under
  `PIPELINE_FLUSH_TIMEOUT_MS` and the overall `SHUTDOWN_TIMEOUT_MS`;
- events accepted but not yet persisted when the process is killed are lost.

PostgreSQL is a required startup dependency because schema migration and repository
initialization happen at boot. A **runtime** database outage does not stop forwarding; it
degrades analytics and becomes visible in metrics and logs.

## Security controls

- **Authentication:** when `PROXY_AUTH_ENABLED=true`, the server requires RFC 1929
  username/password authentication. Empty credentials fail startup validation.
- **Client allowlist:** `PROXY_IP_WHITELIST` accepts exact IPs and CIDR ranges. Invalid
  entries fail startup; they are never silently ignored. Empty means allow all clients.
- **Rate limit:** `RATE_LIMIT_ENABLED` limits new TCP connections per source IP with a token
  bucket. The bucket map is bounded by `RATE_LIMIT_MAX_TRACKED_CLIENTS`.
- **Connection limit:** `PROXY_MAX_CONNECTIONS` is enforced at accept time.
- **Exposure:** proxy/API/metrics bind to `127.0.0.1` by default. Binding the proxy to a
  non-loopback address with authentication disabled logs a warning. The API and metrics
  endpoint have no authentication; keep them loopback or behind a trusted network boundary.
- Credentials are never logged, stored in events or used as metric labels.
- Prometheus labels are bounded enums only; hostnames, IPs and request IDs are not labels.

## Quick start (local, clean checkout)

Prerequisites: Go 1.26.0+, PostgreSQL 15+ (16+ recommended), and a database user.

```bash
git clone <repo-url> socks5-proxy-analytics
cd socks5-proxy-analytics

cp .env.example .env
# Edit .env: at minimum DB_USER, DB_PASSWORD and DB_NAME.
```

Create the database/user (example for a local PostgreSQL superuser):

```bash
psql -d postgres -c "CREATE ROLE socks5 LOGIN PASSWORD 'socks5_local_dev_password';"
psql -d postgres -c "CREATE DATABASE socks5_analytics OWNER socks5;"
```

`configs/config.yml` is optional and gitignored. Start with environment variables only, or
copy the example:

```bash
cp configs/config.example.yml configs/config.yml
```

Run the two processes in separate terminals:

```bash
go run ./cmd/proxy
go run ./cmd/api
```

The proxy listens on `127.0.0.1:1080`, metrics/health on `127.0.0.1:9090`, and the API on
`127.0.0.1:8080`. The schema is migrated automatically at startup.

### Verify a real request

```bash
curl --proxy socks5h://127.0.0.1:1080 http://example.com/
curl -s http://127.0.0.1:9090/metrics | grep socks5_proxy_connections_accepted_total
curl -s 'http://127.0.0.1:8080/logs/traffic?limit=5'
curl -s 'http://127.0.0.1:8080/stats/top-destinations?limit=5'
```

Wait up to `PIPELINE_FLUSH_INTERVAL_MS` before expecting the event in PostgreSQL/API.

### Graceful shutdown

Send `SIGTERM`/`SIGINT` to the proxy process. It stops accepting new connections, waits up
to `PROXY_SHUTDOWN_GRACE_MS` for active sessions (then force-closes them), drains the
pipeline, flushes pending batches, closes the metrics server and database, and logs
`shutdown complete` with `clean=true|false`.

## Docker Compose

The Compose file uses development-only defaults and loopback port bindings:

```bash
docker compose -f deployments/docker-compose.yml up --build
```

Compose reads `.env` from the Compose project directory (the directory containing the
Compose file) by default. To use the repository-root `.env`, pass
`--env-file .env`; shell environment variables also override the defaults.

Published ports: `127.0.0.1:1080` (SOCKS5), `127.0.0.1:8080` (API), `127.0.0.1:9090`
(metrics). PostgreSQL is not published to the host. Override `DB_USER`, `DB_PASSWORD` and
`DB_NAME` via `.env` or the shell for anything beyond local development. Verify:

```bash
curl --proxy socks5h://127.0.0.1:1080 http://api:8080/health
curl -s http://127.0.0.1:9090/metrics | head
```

The proxy container resolves `api` on the Compose network; `socks5h` makes the proxy perform
DNS resolution, which exercises the domain-name path and analytics attribution.

## Configuration reference

Environment variables override the YAML file. `CONFIG_FILE` selects an explicit YAML file;
`CONFIG_PATH` selects a directory (default `./configs`). See
[configs/config.example.yml](configs/config.example.yml) and [.env.example](.env.example).

### Proxy

| Variable | Default | Description |
| --- | --- | --- |
| `PROXY_ADDRESS` | `127.0.0.1` | Listen address |
| `PROXY_PORT` | `1080` | Listen port |
| `PROXY_MAX_CONNECTIONS` | `10000` | Maximum admitted concurrent sessions |
| `PROXY_DIAL_TIMEOUT_MS` | `10000` | Upstream connect timeout |
| `PROXY_HANDSHAKE_TIMEOUT_MS` | `10000` | SOCKS5 negotiation deadline |
| `PROXY_SHUTDOWN_GRACE_MS` | `5000` | Active-session grace before force close |
| `PROXY_IDLE_TIMEOUT_MS` | `0` | Idle timeout during relay (0 disables) |
| `PROXY_AUTH_ENABLED` | `false` | Require RFC 1929 authentication |
| `PROXY_AUTH_USERNAME` / `PROXY_AUTH_PASSWORD` | empty | Required when auth is enabled |
| `PROXY_IP_WHITELIST` | empty | Comma-separated IPs/CIDRs; empty allows all |
| `RATE_LIMIT_ENABLED` | `false` | Enable per-source-IP connection rate limit |
| `RATE_LIMIT_RPS` | `100` | New connections per second per source IP |
| `RATE_LIMIT_MAX_TRACKED_CLIENTS` | `100000` | Bound on rate-limiter state |

### Metrics / API

| Variable | Default | Description |
| --- | --- | --- |
| `METRICS_ENABLED` | `true` | Serve `/metrics` and `/healthz` |
| `METRICS_ADDRESS` / `METRICS_PORT` | `127.0.0.1` / `9090` | Metrics listener |
| `API_ADDRESS` / `API_PORT` | `127.0.0.1` / `8080` | Analytics API listener |
| `API_READ_TIMEOUT_MS` / `API_WRITE_TIMEOUT_MS` / `API_IDLE_TIMEOUT_MS` | `10000` / `10000` / `60000` | HTTP timeouts |

### Policy (Phase 2)

| Variable | Default | Description |
| --- | --- | --- |
| `POLICY_ENABLED` | `false` | Enable deterministic egress policy enforcement |
| `POLICY_ID` | `default` | Policy identifier recorded in audit events |
| `POLICY_VERSION` | `1` | Policy version recorded in audit events |
| `POLICY_DEFAULT_ACTION` | `deny` | Must be `deny` |

Workloads and rules are configured in YAML (`proxy.workloads`, `policy.rules`).

### Database

| Variable | Default | Description |
| --- | --- | --- |
| `DB_HOST` | required | PostgreSQL host |
| `DB_PORT` | `5432` | PostgreSQL port |
| `DB_USER` | required | Database user |
| `DB_PASSWORD` | required | Database password |
| `DB_NAME` | required | Database name |
| `DB_SSLMODE` | `disable` | `disable`, `allow`, `prefer`, `require`, `verify-ca`, `verify-full` |
| `DB_CONNECT_TIMEOUT_MS` | `5000` | Startup ping/migration timeout |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` | `20` / `10` | Connection pool bounds |

### Pipeline / logging / shutdown

| Variable | Default | Description |
| --- | --- | --- |
| `PIPELINE_WORKERS` | `4` | Normalizer workers |
| `PIPELINE_BUFFER_SIZE` | `10000` | Bounded queue capacity per channel |
| `PIPELINE_BATCH_SIZE` | `100` | Records per database batch |
| `PIPELINE_FLUSH_INTERVAL_MS` | `5000` | Maximum batch age |
| `PIPELINE_MAX_RETRIES` | `3` | Additional write attempts (total = retries + 1) |
| `PIPELINE_RETRY_BACKOFF_MS` | `200` | Exponential backoff base |
| `PIPELINE_FLUSH_TIMEOUT_MS` | `5000` | Per-attempt write timeout |
| `SHUTDOWN_TIMEOUT_MS` | `15000` | Overall shutdown/flush deadline |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` | `json` or `text` |

Invalid values, a missing required DB setting, enabled auth with empty credentials, or a
malformed allowlist entry fail startup with an actionable error.

## Metrics

`GET http://127.0.0.1:9090/metrics` (Prometheus text format) and
`GET /healthz` on the same listener. `/healthz` reports proxy process liveness; it is not a
database readiness check. The API process serves PostgreSQL-backed readiness at `GET /health`
and does not serve proxy metrics.

| Metric | Type | Meaning |
| --- | --- | --- |
| `socks5_proxy_active_connections` | gauge | Admitted sessions currently active |
| `socks5_proxy_connections_accepted_total` | counter | Sessions admitted by limits/rules |
| `socks5_proxy_connections_rejected_total{reason}` | counter | Pre-handshake refusals (`max_connections`, `rate_limited`, `ip_not_allowed`, `shutting_down`) |
| `socks5_proxy_auth_failures_total` | counter | Failed username/password attempts |
| `socks5_proxy_handshake_failures_total{reason}` | counter | Handshake/request failures |
| `socks5_proxy_dial_errors_total{reason}` | counter | Upstream dial failures (`dns`, `timeout`, `refused`, `network_unreachable`, `canceled`, `other`) |
| `socks5_proxy_dial_latency_seconds` | histogram | Dial latency |
| `socks5_proxy_connection_duration_seconds` | histogram | Session duration |
| `socks5_proxy_relay_bytes_total{direction}` | counter | Payload bytes by direction |
| `socks5_proxy_relay_errors_total` | counter | Relay copy errors |
| `pipeline_events_collected_total` | counter | Events accepted into the queue |
| `pipeline_events_dropped_total{stage}` | counter | Dropped events |
| `pipeline_queue_events{stage}` | gauge | Current queue occupancy |
| `pipeline_events_normalized_total` | counter | Events converted to records |
| `pipeline_events_published_total` | counter | Records successfully persisted |
| `pipeline_publish_failures_total` | counter | Batches lost after retries |
| `pipeline_events_publish_dropped_total` | counter | Records lost after retries |
| `pipeline_publish_retries_total` | counter | Batch write retries |
| `pipeline_publish_duration_seconds` | histogram | Batch write duration |
| `pipeline_batch_size` | histogram | Records per persisted batch |
| `db_errors_total` | counter | Write errors observed by the publisher |
| `process_shutdowns_total{result}` | counter | `clean` or `timeout` shutdown outcomes |

## Analytics API

All routes are unauthenticated in Phase 1 and bind to loopback by default.

| Route | Description |
| --- | --- |
| `GET /health` | `200` when PostgreSQL is reachable, `503` otherwise |
| `GET /stats/top-destinations?limit=10` | Destinations: requested hostname, else resolved IP |
| `GET /stats/top-domains?limit=10` | Legacy alias for top destinations |
| `GET /stats/top-client-ips?limit=10` | Client IPs by connection count |
| `GET /stats/source-ips?limit=10` | Legacy alias for top client IPs |
| `GET /stats/traffic?start=&end=` | Aggregate bytes, connections, latency, duration |
| `GET /logs/traffic?limit=100&offset=0&start=&end=` | Paginated records |

`limit` is clamped to `1..1000`; `offset` must be non-negative. Timestamps use RFC3339.
The default range is the last 24 hours; ranges longer than 90 days or `end < start` return
`400`.

Example record:

```json
{
  "event_id": "171eb92567d9831ac1a0537b839c43b4",
  "client_ip": "127.0.0.1",
  "client_port": 61567,
  "requested_host": "localhost",
  "destination_ip": "127.0.0.1",
  "destination_port": 18080,
  "protocol": "tcp",
  "outcome": "connected",
  "termination_reason": "upstream_close",
  "timestamp": "2026-10-29T00:13:31.371164+07:00",
  "duration_ms": 4,
  "dial_latency_ms": 0,
  "bytes_client_to_upstream": 87,
  "bytes_upstream_to_client": 195,
  "schema_version": 1
}
```

## Testing and benchmarks

```bash
gofmt -l .                 # expect no output
go build ./...
go vet ./...
go test ./...
go test -race ./...
golangci-lint run ./...
govulncheck ./...
gosec ./...
```

The PostgreSQL integration test is skipped unless `TEST_DB_HOST` is set; a skipped test is
not a pass:

```bash
TEST_DB_HOST=localhost TEST_DB_PORT=5432 TEST_DB_USER=socks5 \
TEST_DB_PASSWORD=change_me TEST_DB_NAME=socks5_analytics_test \
go test -v ./internal/storage
```

Reproducible baseline benchmarks:

```bash
go test -run '^$' -bench . -benchmem -count=3 ./internal/proxy ./internal/pipeline
```

Methodology, environment and measured results are in [docs/benchmarks.md](docs/benchmarks.md).

## Schema and upgrade note

Phase 1 replaced ambiguous legacy columns with explicit fields (`client_ip`,
`requested_host`, `bytes_client_to_upstream`, `bytes_upstream_to_client`, `outcome`,
`termination_reason`, `schema_version`). `AutoMigrate` adds the new columns but does not
drop old ones. Legacy rows keep their old values and should be treated as schema version 0;
historical `source_ip` values may have contained the upstream address. There is no
versioned migration tool or retention policy yet.

## Limitations and non-goals

- Best-effort analytics: no durable replay, no retention policy, no audit guarantee.
- TCP CONNECT only; BIND/UDP ASSOCIATE rejected.
- Idle-session timeout is configurable (`PROXY_IDLE_TIMEOUT_MS`), disabled by default.
- Phase 2 workload identity and destination policy are implemented; policy defaults to deny
  and requires explicit allow rules.
- API and metrics endpoints are unauthenticated; loopback defaults are the Phase 1 boundary.
- `AutoMigrate` remains the schema mechanism.
- Docker Compose build/runtime was verified locally with Colima: images built, all services
  became healthy, a `socks5h` request through the published proxy port reached the API
  container, and the resulting event was persisted and returned by the analytics API.
  Published host ports may need adjustment if 1080/8080/9090 are already in use.
- Not production-ready: no threat-model review, retention policy, rate limit on the API, or
  multi-instance durability.

## License

Provided as-is for educational and professional purposes. See [LICENSE](LICENSE).
