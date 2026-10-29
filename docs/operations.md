# Operations Guide

## 1. Prerequisites

- Go 1.26.0+ for source builds.
- PostgreSQL 15+.
- A database role and database for analytics.

## 2. Installation

```bash
git clone <repo-url> socks5-proxy-analytics
cd socks5-proxy-analytics
cp .env.example .env
```

Create the database:

```bash
psql -d postgres -c "CREATE ROLE socks5 LOGIN PASSWORD 'change_me';"
psql -d postgres -c "CREATE DATABASE socks5_analytics OWNER socks5;"
```

## 3. Configuration

Edit `.env` or `configs/config.yml`. Required database settings:
`DB_HOST`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`. Defaults bind proxy/API/metrics to
loopback. Enable authentication and rate limiting before exposing the proxy.

## 4. Workload and credential setup

For controlled egress, configure workloads in YAML:

```yaml
proxy:
  auth:
    enabled: true
  workloads:
    - id: agent-1
      username: agent
      password: change_me
```

Policy:

```yaml
policy:
  enabled: true
  id: default
  version: 1
  default_action: deny
  rules:
    - id: allow-api
      workload: agent-1
      action: allow
      host: api.example.com
      port: 443
```

Restart the proxy after changes.

## 5. Starting services

```bash
go run ./cmd/proxy
go run ./cmd/api
```

Or Docker Compose:

```bash
docker compose -f deployments/docker-compose.yml up --build
```

## 6. Verifying a permitted connection

```bash
curl --proxy socks5h://127.0.0.1:1080 http://example.com/
```

## 7. Verifying a denied connection

Configure a workload with no allow rule, then attempt a connection and confirm a SOCKS5
reply of `0x02` (not allowed) and an event with `outcome=policy_denied`.

## 8. Inspecting analytics and audit records

```bash
curl -s 'http://127.0.0.1:8080/logs/traffic?limit=5'
curl -s 'http://127.0.0.1:8080/stats/top-destinations?limit=5'
```

## 9. Monitoring health and metrics

- Proxy liveness: `http://127.0.0.1:9090/healthz`
- API readiness (PostgreSQL): `http://127.0.0.1:8080/health`
- Metrics: `http://127.0.0.1:9090/metrics`

## 10. Rotating credentials and changing policy

Update configuration and restart the proxy. Existing connections are not affected by policy
changes; new connections use the new snapshot.

## 11. Upgrading schema and application versions

1. Back up PostgreSQL.
2. Deploy the new binary.
3. Start the process; `AutoMigrate` applies additive schema changes.
4. Verify `/health` and a test connection.

## 12. Backing up and restoring data

Backup:

```bash
pg_dump -Fc -U socks5 -d socks5_analytics -f socks5_analytics.dump
```

Restore:

```bash
pg_restore -U socks5 -d socks5_analytics --clean --if-exists socks5_analytics.dump
```

Validate by running the API and querying `/logs/traffic`.

## 13. Troubleshooting

- Missing DB settings: startup fails with an actionable error; set required variables.
- Policy enabled without auth/workloads: startup fails; configure workloads.
- Database unavailable at runtime: proxy keeps forwarding; API health returns 503 and
  metrics show publish failures.
- Queue drops: inspect `pipeline_events_dropped_total`.

## 14. Security guarantees and limitations

See `docs/security-and-operational-limitations.md`.
