# Security Policy

## Supported versions

Security fixes are applied to the latest release on the `main` branch. Older
releases are not supported.

| Version | Supported |
| --- | --- |
| latest (`main`) | Yes |
| older releases | No |

## Reporting a vulnerability

Please **do not** open a public issue for a suspected security vulnerability.

Report it privately to the maintainer:

- Email: open an issue requesting a private security channel, or contact the
  maintainer through the repository's GitHub profile.

Include, when possible:

- A description of the vulnerability and its impact.
- Steps to reproduce or a proof of concept.
- The affected version or commit.
- Any suggested mitigation.

You can expect an acknowledgement within 7 days and a status update within 30
days. If the issue is confirmed, a fix and coordinated disclosure will follow.

## Security considerations

- The proxy and API bind to `127.0.0.1` by default. Do not expose them to
  untrusted networks without authentication and a network boundary.
- The analytics API and metrics endpoint are unauthenticated; protect them with
  a reverse proxy or network policy when exposed.
- Credentials in configuration are never logged or stored in events.
- `PROXY_AUTH_ENABLED=true` requires both username and password; empty values
  fail startup.
- `PROXY_IP_WHITELIST` entries are validated at startup; invalid entries fail
  rather than being silently ignored.
- This is a best-effort analytics proxy, not a hardened security gateway or a
  durable audit system.

## Scope

In scope: the Go source under `cmd/` and `internal/`, the Docker images, and
the supplied configuration/deployment manifests.

Out of scope: third-party dependencies (report upstream), local misconfiguration,
and issues requiring physical access to the host.