# Environment Configuration

Configuration is loaded from, in increasing priority:

1. built-in safe defaults;
2. an optional YAML file (`CONFIG_FILE`, or `CONFIG_PATH`/`config.yml`, default
   `configs/config.yml`);
3. environment variables (and `.env` loaded by `godotenv` from the working directory).

`CONFIG_FILE` takes precedence over `CONFIG_PATH`. Environment variables always override
both files. Never commit real credentials.

## Quick start

```bash
cp .env.example .env
# Edit DB_USER, DB_PASSWORD, DB_NAME and any optional settings.
```

`configs/config.yml` is gitignored. To use a file instead of environment variables:

```bash
cp configs/config.example.yml configs/config.yml
```

## Required settings

Startup fails with an actionable error unless these are set either in the environment or a
configuration file:

| Variable | Description |
| --- | --- |
| `DB_HOST` | PostgreSQL host |
| `DB_USER` | Database user |
| `DB_PASSWORD` | Database password (non-empty) |
| `DB_NAME` | Database name |

Also validated:

- `DB_SSLMODE` must be one of `disable`, `allow`, `prefer`, `require`, `verify-ca`,
  `verify-full`.
- `PROXY_AUTH_ENABLED=true` requires non-empty `PROXY_AUTH_USERNAME` and
  `PROXY_AUTH_PASSWORD` (max 255 bytes, no NUL).
- Every `PROXY_IP_WHITELIST` entry must be an IP or CIDR.
- Ports, timeouts, queue sizes and connection limits must be positive and within documented
  bounds.

## Complete variable list

See [`.env.example`](../.env.example) for the full list with defaults, and the README's
[configuration reference](../README.md#configuration-reference) for semantics.

Notable defaults:

- Proxy, API and metrics listeners bind to **127.0.0.1**.
- Authentication and rate limiting are **disabled** by default; the allowlist is empty
  (allow all). Enable and configure them before exposing the proxy.
- Metrics are enabled at `127.0.0.1:9090` (`/metrics`, `/healthz`).
- Delivery is best-effort; see the README delivery guarantee.

## Docker

`deployments/docker-compose.yml` sets the in-container bind addresses to `0.0.0.0` and
publishes only loopback ports on the host. It reads `DB_USER`, `DB_PASSWORD` and `DB_NAME`
from the environment/`.env` with development-only defaults. PostgreSQL is not published to
the host.

```bash
docker compose --env-file .env -f deployments/docker-compose.yml up --build
```

## CI

CI starts PostgreSQL and sets `TEST_DB_*` for the integration test:

```text
TEST_DB_HOST, TEST_DB_PORT, TEST_DB_USER, TEST_DB_PASSWORD, TEST_DB_NAME, TEST_DB_SSLMODE
```

If `TEST_DB_HOST` is unset, the storage integration test skips explicitly. A skipped test is
not evidence that PostgreSQL behavior works.

## Troubleshooting

**`critical: DB_HOST environment variable not set` (or USER/PASSWORD/NAME)**
Set the missing value in `.env`, the environment, or the YAML file. Note that `.env` is read
from the process working directory.

**`error reading config file`**
`CONFIG_FILE` points to a missing/unreadable file. Unset it or fix the path.

**`proxy.auth.username is required...`**
Authentication is enabled but credentials are empty. Set them or disable auth.

**`proxy.ip_whitelist entry ... must be an IP address or CIDR`**
Fix or remove the malformed entry; the server refuses to start rather than silently ignoring
part of the allowlist.

**`database ping failed` / `failed to run migrations`**
Check PostgreSQL reachability, credentials, SSL mode and permissions. The proxy and API do
not start without their required schema.
