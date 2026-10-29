# Security and Operational Limitations

## Security limitations

1. **API and metrics are unauthenticated.**
   - Impact: anyone who can reach those listeners can query analytics or scrape metrics.
   - Mitigation: default loopback binds; deploy behind a trusted network boundary or
     reverse proxy with authentication.
   - Blocks release: No, within the declared loopback/local model.

2. **No runtime policy or credential reload.**
   - Impact: policy and workload changes require process restart.
   - Mitigation: restart during a maintenance window; keep connections draining.
   - Blocks release: No.

3. **IPv4-mapped IPv6 is canonicalized in policy evaluation, but host firewall and
   routing isolation remain deployment responsibilities.**
   - Impact: the gateway does not itself prevent all egress outside its process.
   - Mitigation: host/container firewall rules and network namespace isolation.
   - Blocks release: No, documented as a boundary.

4. **Best-effort analytics, not durable audit.**
   - Impact: dropped events or lost batches are possible under overload or process death.
   - Mitigation: metrics and logs expose drops; size queues appropriately.
   - Blocks release: No, explicitly declared.

## Operational limitations

1. **Schema uses AutoMigrate.**
   - Impact: no versioned, ordered, repeatable migration history.
   - Mitigation: back up before upgrade; test restore.
   - Blocks release: No for single-instance upgrades, but documented.

2. **No retention cleanup job.**
   - Impact: storage grows until operator manages it.
   - Mitigation: manual retention SQL or external job.
   - Blocks release: No.

3. **No demonstrated backup/restore automation.**
   - Impact: recovery procedure is operator-driven.
   - Mitigation: PostgreSQL `pg_dump`/`pg_restore` procedure in operations documentation.
   - Blocks release: No, if operators accept the procedure.

4. **No API rate limiting.**
   - Impact: analytics API can be overwhelmed by clients.
   - Mitigation: loopback default and external rate limiting.
   - Blocks release: No, documented.

5. **No multi-instance coordination.**
   - Impact: each process has independent in-memory state.
   - Mitigation: deploy as a single instance per analytics database.
   - Blocks release: No, declared scope.
