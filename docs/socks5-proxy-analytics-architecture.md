# SOCKS5 Proxy Analytics --- Target Architecture

> **Design baseline.** This document describes the intended architecture and may lag the
> implementation. Verified current behavior and evidence are in [audit/correctness.md](audit/correctness.md)
> and [release-readiness.md](release-readiness.md).


**Status:** Architecture baseline for implementation planning\
**Version:** 0.1\
**Date:** 2026-10-28\
**Scope:** Evolve the existing Go SOCKS5 proxy analytics prototype into
a reliable network-observability product, then a controlled egress
gateway for AI agents and automation.

> This document defines the intended direction and the boundaries
> between components. It is not a claim that every component already
> exists or is correctly wired in the current repository. Existing code
> must be inspected before implementation details and interfaces are
> finalized.

## 1. Product direction

The project will evolve in three phases:

1.  **Phase 1 --- Network Observability for SOCKS5 Proxy:** make the
    existing proxy correct, observable, testable, and dependable.
2.  **Phase 2 --- Controlled Egress Gateway:** add explicit workload
    identity and deterministic destination-access policy for AI agents
    and automation.
3.  **Phase 3 --- Productization:** deliver repeatable builds,
    deployable packaging, security documentation, reproducible
    benchmarks, release automation, and evidence from realistic
    validation.

The first milestone is not a rewrite. Keep the current Go implementation
unless repository inspection and measurements demonstrate a concrete
reason to change it. Prefer a modular monolith with clear package
boundaries over premature microservices.

## 2. Architecture principles
1.  **Correctness before feature growth.** Fix attribution, lifecycle,
    shutdown, and runtime wiring before adding new capabilities.
2.  **Keep the data plane lean.** Proxy forwarding must not
    synchronously depend on PostgreSQL, analytics queries, or AI
    inference.
3.  **Enforce security in the runtime.** A defined but unwired
    authentication or policy component does not provide protection.
4.  **Make loss and durability explicit.** Bounded in-memory queues
    protect latency and memory, but may drop events. If durable audit
    delivery is promised, design and test a durable mechanism rather
    than implying an in-memory queue is sufficient.
5.  **Separate telemetry from decisions.** Metrics describe system
    behavior; connection events support analytics and audit; logs
    explain operational failures.
6.  **Deterministic policy.** Authorization decisions must be
    explainable and reproducible. AI may later assist investigation or
    suggest policy, but must not silently grant access.
7.  **Measure before optimizing.** Every performance claim needs a
    reproducible workload, environment description, and benchmark
    result.
8.  **Graceful failure is part of the contract.** Database outages,
    queue saturation, client disconnects, and shutdown must have defined
    behavior.
9.  **Least privilege by default.** Unknown identities, malformed
    policy, and missing required configuration must fail safely.
10. **No architecture by fashion.** Add a component or abstraction only
    when it owns a clear responsibility and contract.

## 3. Target system diagram

``` mermaid
flowchart TB
    subgraph Clients["Clients and Workloads"]
        C1["SOCKS5 clients"]
        C2["AI agents"]
        C3["Automation / CI jobs"]
    end

    subgraph Control["Control Plane — Phase 2"]
        ID["Workload identity"]
        CFG["Configuration and policy store"]
        PE["Deterministic policy engine"]
        PA["Policy decision audit"]
        ADMIN["Admin API / CLI"]
        ADMIN --> CFG
        CFG --> PE
        ID --> PE
        PE --> PA
    end

    subgraph Data["Data Plane — Phase 1 foundation"]
        SOCKS["SOCKS5 protocol handler"]
        AUTH["Authentication and client rules"]
        LIMIT["Connection and rate limits"]
        DEST["Destination validation and routing"]
        UP["Upstream TCP connections"]
        LIFE["Connection lifecycle / byte counters"]
        SOCKS --> AUTH --> LIMIT --> DEST --> UP --> LIFE
    end

    subgraph Observe["Observability Plane — Phase 1"]
        COL["Event collector"]
        NORM["Normalize and enrich"]
        Q["Bounded queue / backpressure"]
        PUB["Batch publisher"]
        DB[("PostgreSQL")]
        API["Analytics API"]
        MET["Prometheus metrics endpoint"]
        LOG["Structured logs"]
        ALERT["Operational alerts"]
        COL --> NORM --> Q --> PUB --> DB --> API
        MET --> ALERT
        LOG --> ALERT
    end

    subgraph Product["Product Engineering — Phase 3"]
        TEST["Unit, integration and E2E tests"]
        BENCH["Reproducible benchmarks"]
        CI["Build, CI and release"]
        DEPLOY["Docker, deployment and docs"]
    end

    C1 --> SOCKS
    C2 --> SOCKS
    C3 --> SOCKS

    LIFE -. "connection events" .-> COL
    SOCKS -. "runtime metrics and logs" .-> MET
    SOCKS -. "runtime diagnostics" .-> LOG

    PE -. "authorization decision" .-> AUTH
    PE -. "destination decision" .-> DEST
    PA -. "policy audit events" .-> COL

    TEST -. "validates" .-> Data
    TEST -. "validates" .-> Observe
    TEST -. "validates" .-> Control
    BENCH -. "measures" .-> Data
    CI -. "builds and ships" .-> Data
    CI -. "builds and ships" .-> Observe
    CI -. "builds and ships" .-> Control
    DEPLOY -. "packages" .-> Data
    DEPLOY -. "packages" .-> Observe
    DEPLOY -. "packages" .-> Control
```

**Diagram interpretation**

-   The **Data Plane** handles SOCKS5 sessions and upstream connections.
-   The **Observability Plane** receives connection events
    asynchronously and persists/query them outside the forwarding path.
-   The **Control Plane** is a Phase 2 capability. It provides identity
    and deterministic policy decisions; it should not become a
    synchronous database dependency on every packet.
-   **Product Engineering** is a cross-cutting delivery and verification
    capability, not a runtime service.

The diagram is logical, not a commitment to separate processes. The
initial implementation should remain a modular monolith unless
operational evidence requires process separation.

## 4. Plane and component responsibilities

### 4.1 Data Plane

  -----------------------------------------------------------------------
  Component               Owns                    Must not own
  ----------------------- ----------------------- -----------------------
  SOCKS5 protocol handler Protocol negotiation,   Database writes,
                          request parsing,        analytics queries,
                          protocol-level errors   policy authoring

  Authentication and      Authenticate the client Hidden or implicit
  client rules            and apply configured    default authorization
                          client-level access     
                          rules                   

  Connection and rate     Enforce configured      Unbounded tracking
  limits                  concurrency and rate    state
                          limits; expose          
                          rejection reasons       

  Destination validation  Validate requested      DNS-dependent
  and routing             host/port and apply the assumptions that erase
                          final destination       the original requested
                          decision                hostname

  Upstream connection     Dial, relay, count      Persisting analytics
  lifecycle               bytes, close resources, synchronously
                          classify termination    

  Connection event        Emit a documented event Blocking the forwarding
  producer                when a session reaches  path on a slow
                          defined lifecycle       analytics consumer
                          points                  
  -----------------------------------------------------------------------

The current repository must be checked to determine which
responsibilities already exist, which are only defined but not wired,
and which need redesign.

### 4.2 Observability Plane

  ------------------------------------------------------------------------------
  Component               Owns                    Required behavior
  ----------------------- ----------------------- ------------------------------
  Event collector         Accept connection and   Bounded, concurrency-safe
                          policy events from      ingress; explicit saturation
                          runtime components      behavior

  Normalizer/enrichment   Normalize timestamps,   Preserve raw facts; never
                          direction, identity,    relabel a destination IP as a
                          destination and outcome client/source IP
                          fields                  

  Queue/backpressure      Isolate proxy work from Fixed capacity; measurable
                          persistence latency     enqueue failures/drops; no
                                                  unbounded memory growth

  Batch publisher         Group events and write  Retry policy, bounded retry
                          batches                 behavior, clear failure
                                                  metrics

  Storage                 Persist queryable event Explicit migration strategy,
                          records and schema      indexes justified by queries,
                          versions                safe startup behavior

  Analytics API           Query summaries and     Input validation, bounded
                          event details           query ranges,
                                                  authentication/authorization
                                                  if exposed

  Metrics endpoint        Expose operational      Actually registered, served,
                          metrics to Prometheus   and updated by runtime paths

  Structured logs         Explain startup,        Avoid secrets and unnecessary
                          policy, connection and  sensitive payloads
                          persistence failures    
  ------------------------------------------------------------------------------

**Important queue contract:** choose and document one of these semantics
for connection analytics:

-   **Best-effort telemetry:** events may be dropped during overload;
    drops are counted and visible.
-   **Durable audit:** accepted audit events must survive the defined
    failure model; this requires a durable mechanism and recovery tests.

Do not call a best-effort in-memory event pipeline "durable audit."

### 4.3 Control Plane --- Phase 2

  -----------------------------------------------------------------------
  Component               Owns                    Guardrails
  ----------------------- ----------------------- -----------------------
  Workload identity       Resolve a connection to Identity source and
                          a stable workload       trust assumptions must
                          identity                be documented

  Configuration/policy    Store versioned policy  Validate before
  store                   and configuration       activation; define
                                                  reload and rollback
                                                  behavior

  Deterministic policy    Decide whether an       Explicit default-deny
  engine                  identity may reach a    behavior where policy
                          destination and port    is required; stable
                                                  decision reasons

  Policy decision audit   Record policy version,  Must distinguish
                          identity, target,       attempted, accepted,
                          decision and reason     rejected, and
                                                  successfully persisted
                                                  records

  Admin API/CLI           Inspect and update      Authenticated
                          configuration/policy    administrative actions;
                                                  validate before apply
  -----------------------------------------------------------------------

Policy evaluation should have a small, explicit interface. The runtime
should evaluate against a validated in-memory policy snapshot where
feasible. Database reads should not occur for every packet or every
relay operation.

### 4.4 Product Engineering --- Phase 3

-   Unit tests for parsing, policy, accounting, queue semantics, and
    lifecycle state transitions.
-   Integration tests for proxy-to-upstream behavior, event persistence,
    migrations, metrics and configuration.
-   End-to-end tests that exercise a client through the SOCKS5 listener
    and verify observable outcomes.
-   Failure-injection tests for database unavailability, full queues,
    upstream failures, client disconnects and shutdown.
-   Reproducible benchmarks with documented hardware/software, workload
    shape, concurrency, duration and measurement methodology.
-   CI for formatting, static checks, tests, race detection, builds and
    release artifacts.
-   Docker/deployment configuration that works with its shipped defaults
    and documents secrets/configuration.
-   Security model, operational runbook, configuration reference,
    troubleshooting guide and release notes.

## 5. Core data contracts

These contracts are semantic requirements. Exact Go structs and database
columns must be confirmed against the current code before
implementation.

### 5.1 Connection event

A normalized connection event should be able to represent at least:

-   Event ID or another documented deduplication identity.
-   Event timestamp and, if useful, event type.
-   Client/source IP and port, where available.
-   Original requested destination hostname, if supplied by the SOCKS5
    request.
-   Resolved destination IP, if resolution occurred and the value is
    available.
-   Destination port and transport/protocol.
-   Workload/client identity, when available.
-   Connection outcome and termination reason.
-   Bytes client-to-upstream and bytes upstream-to-client, with
    direction defined unambiguously.
-   Duration and lifecycle timestamps where useful.
-   Policy decision, policy version and decision reason when Phase 2 is
    enabled.
-   Schema/event version for safe evolution.

**Attribution rule:** client/source address, requested hostname, and
resolved destination address are distinct concepts. Do not overload one
field to represent another. Preserve the requested hostname before DNS
resolution.

**Byte accounting rule:** define the direction from the client's
perspective and test it with known payloads in both directions. Document
whether counters include protocol negotiation bytes or only relayed
application bytes.

### 5.2 Policy decision

A policy decision should include:

-   Stable identity or principal.
-   Requested hostname/IP and port.
-   Allow/deny result.
-   Stable machine-readable reason code.
-   Policy version or snapshot identity.
-   Decision timestamp.
-   Relevant request/connection correlation ID.

Do not log credentials or secrets. Avoid retaining more identifying data
than the product's documented purpose requires.

### 5.3 Metrics

At minimum, assess and implement metrics for:

-   Active connections and accepted/rejected connections.
-   Connection outcomes and termination reasons.
-   Dial/relay errors and latency.
-   Bytes in each direction.
-   Event enqueue failures/drops and queue occupancy.
-   Publisher batch size, duration, failures and retries.
-   Database operation latency/errors.
-   Policy allow/deny counts and evaluation latency when Phase 2 exists.
-   Process health and shutdown/flush outcomes.

Use bounded-cardinality labels. Do not use arbitrary destination
hostnames, client IPs, or request IDs as Prometheus labels.

## 6. Runtime and lifecycle contracts

### 6.1 Forwarding path

1.  Parse and validate the SOCKS5 request.
2.  Authenticate the client and apply client-level rules.
3.  Apply connection/rate limits.
4.  Preserve the requested hostname and validate the destination.
5.  In Phase 2, evaluate the applicable deterministic destination policy
    before dialing upstream.
6.  Dial upstream and relay traffic.
7.  Count both byte directions and record lifecycle outcome.
8.  Emit the event without making the relay wait for database
    persistence.

The precise ordering of authentication, limits, DNS resolution and
policy evaluation must be validated against SOCKS5 behavior and the
chosen policy semantics before coding.

### 6.2 Shutdown

The application should have a defined, testable shutdown sequence:

1.  Stop accepting new connections.
2.  Signal active sessions according to the configured grace period.
3.  Stop new event production after connection handling reaches its
    shutdown boundary.
4.  Close the event queue only after producers are done, or use another
    concurrency-safe ownership protocol.
5.  Flush pending batches within a bounded deadline.
6.  Close database and other resources.
7.  Report whether shutdown completed cleanly or timed out.

Do not cancel the publisher's context before its final flush has had a
chance to complete. Goroutine ownership, channel closing, and
cancellation rules must be explicit to prevent send-on-closed-channel
races and leaked goroutines.

### 6.3 Overload and dependency failure

-   A full analytics queue must never cause unbounded memory growth.
-   Best-effort telemetry may be dropped only under documented semantics
    and with visible drop counters.
-   Persistence failures must not silently look like successful
    delivery.
-   Proxy availability during database failure must follow an explicit
    configuration/product decision.
-   Security enforcement failures must fail according to a documented
    safe policy; they must not silently bypass authentication or
    destination restrictions.

## 7. Phase roadmap and exit criteria

### Phase 1 --- Network Observability foundation

**Goal:** make the current SOCKS5 proxy correct, observable, stable and
testable.

Workstreams:

1.  **Repository baseline:** map actual packages, runtime entry points,
    configuration, database schema and existing tests.
2.  **Attribution correctness:** fix source/client IP semantics,
    requested hostname capture, resolved destination fields and byte
    direction.
3.  **Runtime security wiring:** verify authentication and client rules
    are called from the actual request path; enforce configured
    connection and rate limits.
4.  **Observability wiring:** register and expose Prometheus metrics;
    connect real runtime paths to counters/gauges/histograms and
    structured logs.
5.  **Pipeline contract:** define queue capacity, drop/backpressure
    behavior, batching, retries and failure metrics.
6.  **Lifecycle reliability:** fix shutdown ordering, final batch flush
    and goroutine termination.
7.  **Configuration/deployment:** fix environment/config overrides and
    ensure shipped Docker Compose defaults are coherent.
8.  **Verification:** add focused unit, integration and E2E tests; run
    race detection; add baseline benchmarks.

**Phase 1 exit criteria**

-   A real SOCKS5 client can complete a connection through the proxy to
    a controlled test upstream.
-   Recorded client address, requested hostname, resolved destination
    and byte directions are verified by tests.
-   Security controls that are advertised as enabled are demonstrably
    enforced in the runtime.
-   `/metrics` or the chosen documented metrics endpoint is reachable
    and reflects real activity.
-   Queue overload and persistence failure behavior are documented and
    tested.
-   Shutdown tests verify pending events are handled according to the
    selected delivery contract.
-   A clean checkout can be built, tested and run using documented steps
    and configuration.
-   Benchmark results are reproducible enough to compare future changes.

### Phase 2 --- Controlled Egress Gateway

**Goal:** enable explicit, auditable control over where identified
workloads can connect.

Workstreams:

1.  Define workload identity and trust boundaries.
2.  Define a versioned policy model for identities, destination
    hosts/IPs, ports and decisions.
3.  Implement deterministic evaluation and stable reason codes.
4.  Enforce decisions in the actual SOCKS5 request path before upstream
    dialing.
5.  Add policy audit events and policy-related metrics.
6.  Define configuration validation, safe activation, reload and
    rollback.
7.  Add tests for default behavior, malformed policy, unknown identity,
    DNS edge cases and policy changes.

**Phase 2 exit criteria**

-   Policy behavior is deterministic and covered by tests.
-   Every policy decision has a clear outcome and reason.
-   The runtime does not bypass required policy when policy data is
    unavailable or invalid.
-   Policy versioning and rollback are documented and tested.
-   Audit delivery guarantees are accurately stated and verified.
-   Added policy evaluation overhead is measured under a reproducible
    workload.

### Phase 3 --- Productization and real-world validation

**Goal:** deliver a maintainable, installable and supportable product.

Workstreams:

1.  Stabilize public configuration and compatibility expectations.
2.  Produce release artifacts and a documented upgrade path.
3.  Harden admin/API access, secrets handling and operational defaults.
4.  Complete CI, release automation, dependency checks and security
    review.
5.  Publish reproducible benchmarks with methodology and raw results.
6.  Document architecture, threat model, deployment, operations,
    troubleshooting and limitations.
7.  Run a realistic pilot or controlled deployment; record findings and
    resolve critical issues.
8.  Define versioning, changelog, support expectations and rollback
    procedures.

**Phase 3 exit criteria**

-   A new user can deploy from documented instructions without private
    tribal knowledge.
-   CI verifies the release candidate from a clean checkout.
-   Benchmarks can be rerun and compared.
-   Security boundaries, failure modes and data-retention behavior are
    documented.
-   A real deployment/pilot has produced actionable evidence, not just a
    successful local demo.
-   Release and rollback procedures are tested.

## 8. Testing strategy

  ------------------------------------------------------------------------
  Test level              What it proves          Examples
  ----------------------- ----------------------- ------------------------
  Unit                    Local logic and         SOCKS5 parsing, byte
                          invariants              accounting,
                                                  normalization, policy
                                                  evaluation, config
                                                  validation

  Component               A component's contract  Queue saturation,
                          under realistic         publisher
                          dependencies            batching/retry, DB
                                                  migration, metrics
                                                  registration

  Integration             Components work         Proxy → upstream → event
                          together                pipeline → database;
                                                  config → runtime
                                                  enforcement

  End-to-end              Product behavior from a SOCKS5 handshake,
                          client's perspective    allowed/denied
                                                  destination, metrics
                                                  change, persisted event

  Failure injection       Defined failure         DB down, upstream
                          behavior is real        timeout, full queue,
                                                  abrupt disconnect,
                                                  shutdown during batch

  Race/stress             Concurrency is safe and `go test -race ./...`,
                          stable                  repeated
                                                  connect/disconnect,
                                                  concurrent shutdown

  Benchmark               Performance claims have Throughput, latency
                          evidence                percentiles, CPU/memory,
                                                  event pipeline overhead
  ------------------------------------------------------------------------

Tests should assert behavior, not merely that a function returns no
error. Use controlled local upstream servers for repeatable networking
tests.

## 9. Security and privacy baseline

-   Authentication and access controls must be enforced by the live
    request path.
-   Default behavior for unknown clients/workloads and invalid policy
    must be explicit and safe.
-   Do not store credentials, authentication tokens, or proxy secrets in
    events or logs.
-   Validate and bound user-controlled inputs, query ranges, connection
    counts and queue capacity.
-   Protect any remotely exposed analytics/admin endpoint with
    appropriate authentication and authorization.
-   Use bounded-cardinality metrics and avoid exporting sensitive
    per-client or per-destination values as labels.
-   Document which connection metadata is collected, why it is needed,
    how long it is retained and who can query it.
-   Define whether hostname/IP data is considered sensitive in the
    deployment context.
-   Treat database and policy-store access as separate trust boundaries.
-   Add threat-model review before calling the product production-ready.

## 10. Open decisions --- do not silently assume

These decisions require repository inspection, a stated product
requirement, or measurement:

1.  **Runtime shape:** keep one process for proxy, API and pipeline
    initially, or split a component only when evidence requires it.
2.  **Event delivery:** best-effort telemetry versus durable audit;
    these are different guarantees.
3.  **Database role:** required dependency versus optional analytics
    sink, including expected behavior during outage.
4.  **Destination policy semantics:** hostname matching, DNS resolution,
    IP/CIDR matching, redirects/re-resolution and DNS rebinding
    considerations.
5.  **Identity source:** SOCKS5 credentials, local configuration,
    workload-specific credentials, or another mechanism.
6.  **Connection/rate limit scope:** per client, identity, source IP,
    destination, or global.
7.  **Metrics endpoint exposure:** bind address, authentication/network
    boundary and deployment assumptions.
8.  **Analytics API scope:** local-only, internal network, or externally
    accessible.
9.  **Retention and migrations:** data-retention policy, schema
    migration approach and upgrade compatibility.
10. **Performance targets:** expected concurrent connections,
    throughput, latency budget, CPU/memory budget and test environment.

Record each decision in an ADR when it affects security, compatibility,
persistence semantics or package boundaries.

## 11. Implementation sequence

Do not start by building every box in the diagram. Proceed in dependency
order:

1.  Inventory the current repository and map actual code to this target
    architecture.
2.  Establish a reproducible baseline: build, tests, race checks,
    current runtime behavior and baseline benchmark.
3.  Write down event field semantics and queue/shutdown contracts.
4.  Fix attribution correctness and add regression tests.
5.  Wire and verify the security controls that already exist in code.
6.  Wire metrics and structured operational diagnostics.
7.  Fix event-pipeline overload, persistence failure and shutdown
    behavior.
8.  Repair configuration and deployment defaults.
9.  Add integration/E2E tests and repeatable benchmarks.
10. Review Phase 1 exit criteria before designing Phase 2 policy
    implementation in detail.
11. Add Phase 2 only after identity, policy semantics and failure
    behavior have explicit decisions.
12. Complete Phase 3 packaging, docs, security review and pilot
    validation.

Each implementation task should include: **scope, contract,
files/packages allowed to change, tests, verification commands,
evidence, documentation impact, and explicit non-goals.** Keep changes
small enough to review and revert independently.

## 12. Definition of done

A feature is not done merely because it compiles. It is done when:

-   Its behavior and failure semantics are specified.
-   The real runtime path invokes it.
-   Relevant unit and integration tests cover success and failure cases.
-   Concurrency-sensitive code passes race checks.
-   Metrics/logging make important failures observable without leaking
    secrets.
-   Configuration and deployment behavior are documented.
-   Performance claims are backed by reproducible measurements.
-   Review confirms the change stays within scope and preserves existing
    contracts.

------------------------------------------------------------------------

## Appendix A --- Current-repository verification checklist

Use this checklist during the first repository-mapping pass. Mark each
item `confirmed`, `missing`, `partially wired`, or
`needs investigation`.

-   [ ] SOCKS5 listener and protocol handler entry point
-   [ ] Authentication path and default behavior
-   [ ] Client/IP allowlist path and actual enforcement
-   [ ] Connection limit and rate limit enforcement
-   [ ] Requested hostname preservation before DNS resolution
-   [ ] Client/source IP and upstream destination IP stored separately
-   [ ] Byte counters have explicit
    client-to-upstream/upstream-to-client semantics
-   [ ] Connection lifecycle and termination reason handling
-   [ ] Event collector and concurrency ownership
-   [ ] Queue capacity, overload behavior and drop counters
-   [ ] Publisher batching, retry and persistence failure behavior
-   [ ] Database schema and migration path
-   [ ] Analytics API authentication and query limits
-   [ ] Prometheus registry and reachable metrics endpoint
-   [ ] Structured logging and secret redaction
-   [ ] Graceful shutdown and final event flush
-   [ ] Docker Compose configuration and environment overrides
-   [ ] Unit/integration/E2E test coverage
-   [ ] Race test and repeatable baseline benchmark
-   [ ] CI Go version matches the module and deployment image
