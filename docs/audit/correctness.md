# Correctness Contract Audit (Phase 1)

This document records the behavior that was verified against the implementation, the
discrepancies found, and the limits of the evidence. Test names refer to the Go test suite in
this repository.

## 1. SOCKS5 protocol

### 1.1 Method negotiation (RFC 1928 §3)

| Contract | Evidence | Status |
| --- | --- | --- |
| Only version `0x05` is accepted; other versions are rejected | `TestMalformedGreetingIsReported/bad_version` | verified |
| A zero-method greeting is rejected with `0xFF` | `TestMalformedGreetingIsReported/zero_methods` | verified |
| No-auth mode selects `0x00` only when offered | `openSOCKS` in `server_test.go`, `TestCONNECT...` | verified |
| Auth mode requires `0x02`; offering only `0x00` yields `0xFF` | `TestAuthenticationRejectsClientWithoutUserPassMethod` | verified |
| Unknown methods are never selected | `negotiateAuth` selects only the configured method | verified by inspection |
| A partial greeting is bounded by `handshake_timeout_ms` | `TestMalformedGreetingIsReported/truncated*` | verified |

### 1.2 Username/password authentication (RFC 1929)

| Contract | Evidence | Status |
| --- | --- | --- |
| Correct credentials succeed | `TestAuthenticationIsEnforced` | verified |
| Wrong password fails with status `0x01` | `TestAuthenticationIsEnforced` | verified |
| Unknown username fails with status `0x01` | `AuthenticateWithIdentity` | verified by inspection |
| Zero-length username is rejected | `authenticateUserPass` | verified by inspection |
| Truncated auth exchange fails without hanging | `TestTruncatedAuthIsReported` | verified |
| Passwords are never logged | no log statement references password fields; `gosec` clean | verified by inspection |
| Workload identities replace legacy credentials | `TestWorkloadOnlyAuthenticationAttributesIdentity` | verified |
| Credentials are compared in constant time | `crypto/subtle.ConstantTimeCompare` | verified by inspection; username lookup is not constant-time |

Limits: RFC 1929 limits username/password to 255 bytes; the parser reads the declared length
(one byte) and configuration validation rejects oversized configured credentials.

### 1.3 CONNECT request parsing

| Contract | Evidence | Status |
| --- | --- | --- |
| IPv4 literal | `TestCONNECTIPLiteralHasEmptyRequestedHost` | verified |
| IPv6 literal | `TestCONNECTIPv6PreservesAttribution` | verified |
| Domain name | `TestCONNECTPreservesAttributionAndByteDirections` | verified |
| `BIND` (0x02) rejected with reply `0x07` | `TestUnsupportedCommandAndAddressType` | verified |
| `UDP ASSOCIATE` (0x03) rejected with reply `0x07` | `readRequest` rejects every command except CONNECT | verified by inspection; no dedicated test for 0x03 |
| Unsupported address type rejected with `0x08` | `TestUnsupportedCommandAndAddressType` | verified |
| Zero-length domain rejected | `TestMalformedRequestVariants/domain_length_zero` | verified |
| Port zero rejected | `TestMalformedRequestVariants/port_zero` | verified |
| Non-zero RSV byte rejected | `readRequest` | verified by inspection; no dedicated test |
| Truncated request bounded by the handshake deadline | `TestMalformedRequestVariants/truncated_request_header` | verified |

The reply's `BND.ADDR/BND.PORT` is the proxy's local upstream address
(`upstream.LocalAddr()`), which is the RFC-recommended value for a CONNECT reply. Failure
replies use `0.0.0.0:0`.

### 1.4 Upstream failures and reply mapping

| Condition | Reply | Evidence |
| --- | --- | --- |
| Connection refused | `0x05` | `TestDialFailureEmitsEventAndReply` |
| Network unreachable | `0x03` | `replyCodeForDialError` by inspection |
| DNS failure | `0x04` | `replyCodeForDialError` by inspection |
| Timeout | `0x04` | `replyCodeForDialError` by inspection |
| Other | `0x01` | `TestDialFailureEmitsEventAndReply` accepts refusal or general failure |

The mapping is derived from error strings for refused/unreachable/no-such-host, with typed
checks for DNS, timeout and cancellation first. Error-string classification is inherently
platform-dependent; the typed checks cover the common cases.

### 1.5 Concurrency and admission

| Contract | Evidence | Status |
| --- | --- | --- |
| Per-source-IP token bucket | `TestRateLimitEnforcedPerClient` | verified |
| `max_connections` enforced | `TestMaxConnectionsEnforced` | verified |
| CIDR allowlist rejects before the handshake | `TestIPWhitelistRejectsBeforeHandshake` | verified |
| Rejections are counted with bounded labels | `TestIPWhitelistRejectsBeforeHandshake`, `TestMaxConnectionsEnforced` | verified |
| Rate-limiter memory is bounded | `SetMaxClients` + LRU eviction; `BenchmarkRateLimiterSaturatedNewClient` | verified |
| Rate-limiter eviction is O(1) | LRU list; 186 µs → 293 ns per new client at capacity | verified |

Pre-handshake rejections do not produce analytics events (there is no session to attribute).
This is intentional and documented in the README.

## 2. Attribution and accounting

| Field | Definition | Evidence |
| --- | --- | --- |
| `client_ip` / `client_port` | SOCKS5 TCP peer | `TestCONNECTPreservesAttributionAndByteDirections` |
| `requested_host` | Domain from the request; empty for IP literals | `TestCONNECTIPLiteralHasEmptyRequestedHost` |
| `destination_ip` | IP actually dialed (peer of the upstream socket) | `TestCONNECTIPv6PreservesAttribution`, E2E test |
| `destination_port` | Requested port | all CONNECT tests |
| `bytes_client_to_upstream` | Payload written to the upstream socket | `TestCONNECT...`, concurrent test |
| `bytes_upstream_to_client` | Payload written to the client socket | `TestCONNECT...` |
| Handshake bytes excluded | relay starts after the success reply | `TestCONNECT...` byte assertions |
| `duration_ms` | accept → session finalization | `TestCONNECT...` (non-negative, consistent) |
| `dial_latency_ms` | upstream dial only | `dialUpstream` by inspection |

Byte counters count bytes **successfully written** to the destination, not bytes read from the
source. A partial write counts only the bytes accepted by the socket. This is the useful
definition for traffic accounting and matches the metric help text.

Controlled-payload tests use exact byte counts (13/8, 4/4, 5/9 bytes) and a 20-way concurrent
test that asserts independent per-session counters and unique client ports
(`TestConcurrentAttribution`).

The end-to-end load harness additionally compares the proxy's
`socks5_proxy_relay_bytes_total` deltas with the bytes the workload generator sent and received.
Observed deltas were zero for 4 KiB echo, 64 KiB upload and 64 KiB download workloads
(`docs/benchmarks.md`).

## 3. Event delivery semantics

The implementation distinguishes these states, and the metrics map to them:

| State | Signal |
| --- | --- |
| Session admitted | `socks5_proxy_connections_accepted_total`, `socks5_proxy_active_connections` |
| Event constructed | not separately observable; an event ID allocation failure is counted as a collector drop |
| Event entered the pipeline | `pipeline_events_collected_total` |
| Event normalized | `pipeline_events_normalized_total` |
| Batch written | `pipeline_events_published_total`, `pipeline_batch_size`, `pipeline_publish_duration_seconds` |
| Event dropped | `pipeline_events_dropped_total{stage="collector"}`, `pipeline_events_publish_dropped_total`, `pipeline_publish_failures_total` |
| Shutdown incomplete | `process_shutdowns_total{result="timeout"}` plus an error log |

Verified properties:

- Exactly one event per **admitted** session, including handshake, auth, dial and policy
  failure paths (`TestAuthenticationIsEnforced`, `TestDialFailureEmitsEventAndReply`,
  `TestPolicyDefaultDenyWithoutMatchingRule`).
- Queue saturation drops are counted and logged with a bounded log rate
  (`TestCollectorDropsWhenQueueFull`; load test observed 20,461 collector drops during a
  database outage).
- Exhausted retries are counted as loss and never as published
  (`TestPublisherReportsExhaustedRetriesAsLoss`).
- Shutdown deadline aborts retries, cancels the in-flight write, drains the queue and counts
  every discarded record (`TestPublisherAbortCancelsInFlightWriteAndCountsLoss`,
  `TestPipelineShutdownAbortAccountsForQueuedEvents`).
- Retries cannot create duplicate rows: `event_id` is unique and inserts use
  `ON CONFLICT (event_id) DO NOTHING` (`TestPostgresRepositoryIntegration` re-inserts the same
  batch).

**Guarantee: best-effort, at-most-once per event ID, no durable queue.** There is no disk
spool, no acknowledgment, and no replay. A process crash, a `SIGKILL`, or an exhausted
shutdown deadline loses queued events. The loss is observable through the counters above, but
the exact number lost on a crash cannot be recovered.

## 4. Policy correctness

### 4.1 Decision path

`internal/proxy/server.go:evaluatePolicy` performs, for every request when policy is enabled:

1. If the request is a domain, resolve it **once** with `net.Resolver.LookupIP` under
   `dial_timeout_ms`.
2. Evaluate each returned candidate address against the policy.
3. Choose the first allowed candidate.
4. Set `req.dialHost` to the **chosen IP literal** and dial that exact address.

This closes the time-of-check/time-of-use gap for domain requests: the address evaluated by
policy is the address dialed, with no second resolution. DNS rebinding between evaluation and
dial is therefore not possible for a request that policy admits. A resolution failure is
recorded as `dns_resolution_failed` and denied.

### 4.2 Verified contracts

| Contract | Evidence | Status |
| --- | --- | --- |
| Policy disabled → no policy evaluation | `TestCONNECT...` with `nil` engine | verified |
| Policy enabled + no matching rule → deny | `TestPolicyDefaultDenyWithoutMatchingRule` | verified |
| Default action must be `deny` | `Policy.Validate`, config validation | verified |
| Missing workload identity → deny | `Engine.Evaluate` | verified by inspection |
| Deny rule beats allow rule | `selectRules` | verified by inspection; covered by policy unit tests |
| Higher priority wins; ties by declaration order | `higherPriority` | verified by policy unit tests |
| Workload matching is case-insensitive; `*`/empty matches all | `workloadMatches` | verified by inspection |
| Host suffix matching does not match `evil-example.com` for `example.com` | `hostMatches` | verified by inspection |
| IPv4-mapped IPv6 is canonicalized before matching | `canonicalIP` | verified by policy unit tests |
| Restricted destinations require an explicit IP allow rule | `Engine.Evaluate` | verified by policy unit tests |
| Policy evaluation failure is fail-closed | `Evaluate` returns deny for missing policy/identity | verified |
| `policy.enabled` without an engine is rejected at construction | `TestPolicyEnabledWithoutEngineIsRejected` | verified |
| `policy.enabled` requires auth + workloads | `Config.validatePolicy` | verified |

### 4.3 Policy risk review

- **DNS resolution is bounded** by `dial_timeout_ms` and uses a single lookup; it is not
  bounded by `proxy.shutdown_grace_ms` because it derives from `context.Background()`. During
  shutdown a lookup can run up to `dial_timeout_ms`. This is bounded but not cancellable by
  shutdown.
- **Restricted CIDRs are a denylist layered under an allowlist.** The default restricted set is
  loopback, link-local, `0.0.0.0/8` and `::/128`. Private RFC 1918 ranges and cloud metadata
  addresses other than `169.254.0.0/16` (for example `fd00:ec2::254`) are **not** restricted by
  default. Deployments that must prevent access to internal networks must add those ranges to
  `policy.restricted_cidrs`. This is a documented configuration responsibility, not a bypass
  of the engine: an allow rule that explicitly covers the IP is required either way.
- **Hostname rules only match domain requests.** A client that connects to an IP literal cannot
  match a `host`/`host_suffix` rule; such requests need an IP rule. This is fail-closed.
- **Trailing-dot hostnames** (`example.com.`) do not match suffix rules and are denied. This is
  fail-closed but may surprise operators.
- **Policy rules referencing an unknown workload** are accepted and never match. Startup
  validation does not currently cross-check rule workload IDs against configured workloads.
  This is fail-closed (default deny) but produces silent misconfiguration. Documented as a
  known limitation.

## 5. Lifecycle and shutdown

| Contract | Evidence | Status |
| --- | --- | --- |
| Stop accepting new connections first | `Server.Shutdown` closes the listener | verified |
| Wait up to `proxy.shutdown_grace_ms`, then force-close | `TestShutdownClosesActiveSessionsAndEmitsEvent`, measured 2.05 s with a 2 s grace | verified |
| Forced sessions still produce events with `server_shutdown` | `TestShutdownClosesActiveSessionsAndEmitsEvent` | verified |
| Pipeline drains and flushes after the proxy stops | `TestPipelineShutdownFlushesPendingEvents`, shutdown log `clean=true` with no active sessions | verified |
| Shutdown deadline aborts retries and accounts for loss | `TestPipelineShutdownAbortAccountsForQueuedEvents` | verified |
| Idle timeout closes silent sessions | `TestIdleTimeoutClosesSilentSession` | verified |
| Upstream close finalizes the session even if the client stays open | `TestUpstreamCloseFinalizesSessionWithoutClientClose` | verified |
| Client half-close still delivers the upstream response | `TestClientHalfCloseStillDeliversUpstreamResponse` | verified |

The relay lingers for at most `relayLingerTimeout` (5 s) after the upstream half finishes before
closing a client that never closes. This bounds descriptor and goroutine retention.

## 6. Discrepancies fixed in this audit

| # | Defect | Fix | Regression test |
| --- | --- | --- | --- |
| 1 | Session never finalized when the upstream closed first and the client stayed open | Bounded linger, then close the client | `TestUpstreamCloseFinalizesSessionWithoutClientClose` |
| 2 | `policy.enabled` with a nil engine silently disabled policy | `NewServer` returns an error | `TestPolicyEnabledWithoutEngineIsRejected` |
| 3 | Concurrent first-time migrations failed | PostgreSQL advisory lock around `AutoMigrate` | `TestConcurrentMigrations` |
| 4 | Workload-only auth failed validation | Legacy credentials optional when workloads exist; workloads require auth | `TestValidateAcceptsWorkloadOnlyAuthentication` |
| 5 | Truncated handshakes reported as malformed input | Underlying I/O error is preserved and timeouts are classified | `TestMalformedGreetingIsReported`, `TestMalformedRequestVariants` |
| 6 | Publisher retries outlived the shutdown deadline | Abort channel + cancelable write context + drop accounting | `TestPublisherAbortCancelsInFlightWriteAndCountsLoss` |
| 7 | `active_connections` gauge could move backwards under concurrency | Gauge uses `Inc`/`Dec` | covered by existing concurrency tests |
| 8 | API pagination offset unbounded | Capped at 1,000,000 | `internal/handlers` validation tests |
| 9 | Integration tests in two packages shared one database and raced | Each integration test creates and drops a throwaway database | `TestPostgresRepositoryIntegration`, `TestProxyPipelinePostgresE2E` (3 consecutive `-race` runs) |
| 10 | Truncated-input tests raced the client's own 2 s deadline against the proxy's 2 s handshake timeout | Client deadline raised to 5 s so the proxy classification is deterministic | `go test -run 'TestMalformed\|TestTruncatedAuth' -count=10` |

## 7. Not verified / limitations

- No test exercises a real `UDP ASSOCIATE` or `BIND` success path (they are unsupported by
  design); only rejection is tested.
- No fuzzing of the handshake parser. All reads are length-prefixed and bounded (2, 255, 4,
  1+255, 2 bytes), but a fuzz target is recommended before major parser changes.
- No test covers `SO_LINGER`/RST behavior on abrupt client death beyond the close-after-connect
  case.
- The relay linger (5 s) is a constant, not configurable. It bounds the leak but is not tuned
  per deployment.
- IPv6 upstream connectivity was verified only on loopback (`::1`).
- The analytics delivery guarantee is best-effort; durability requires an external queue and is
  out of scope.
- DNS behavior for multi-address names is exercised only indirectly (no controllable resolver
  in tests). The code path resolves once and dials the evaluated address, but a test with a
  fake resolver would make that invariant explicit.
