package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/models"
)

func (m model) renderOverview() string {
	var b strings.Builder
	s := m.state.Metrics

	// Runtime
	b.WriteString(sectionTitle("Proxy runtime"))
	b.WriteString("\n")
	active, ok := s.Scalar("socks5_proxy_active_connections")
	b.WriteString(fmt.Sprintf("  Active connections: %s\n", FormatInt(active, ok && s.OK)))
	accepted, ok := s.Scalar("socks5_proxy_connections_accepted_total")
	b.WriteString(fmt.Sprintf("  Accepted connections: %s\n", FormatInt(accepted, ok && s.OK)))
	rejected, ok := s.VectorSum("socks5_proxy_connections_rejected_total")
	b.WriteString(fmt.Sprintf("  Rejected connections: %s\n", FormatInt(rejected, ok && s.OK)))
	authFail, ok := s.Scalar("socks5_proxy_auth_failures_total")
	b.WriteString(fmt.Sprintf("  Auth failures: %s\n", FormatInt(authFail, ok && s.OK)))
	hsFail, ok := s.VectorSum("socks5_proxy_handshake_failures_total")
	b.WriteString(fmt.Sprintf("  Handshake failures: %s\n", FormatInt(hsFail, ok && s.OK)))
	dialErr, ok := s.VectorSum("socks5_proxy_dial_errors_total")
	b.WriteString(fmt.Sprintf("  Dial errors: %s\n", FormatInt(dialErr, ok && s.OK)))
	relayErr, ok := s.Scalar("socks5_proxy_relay_errors_total")
	b.WriteString(fmt.Sprintf("  Relay errors: %s\n", FormatInt(relayErr, ok && s.OK)))

	// Throughput
	b.WriteString("\n")
	b.WriteString(sectionTitle("Throughput (derived between scrapes)"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  Client→upstream: %s\n", FormatRate(m.rates.ClientToUpstreamPerSec, m.rates.Valid)))
	b.WriteString(fmt.Sprintf("  Upstream→client: %s\n", FormatRate(m.rates.UpstreamToClientPerSec, m.rates.Valid)))
	c2u, ok := s.LabelValue("socks5_proxy_relay_bytes_total", "client_to_upstream"), s.Has("socks5_proxy_relay_bytes_total")
	b.WriteString(fmt.Sprintf("  Total client→upstream bytes: %s\n", FormatBytesOrNA(c2u, ok)))
	u2c, ok := s.LabelValue("socks5_proxy_relay_bytes_total", "upstream_to_client"), s.Has("socks5_proxy_relay_bytes_total")
	b.WriteString(fmt.Sprintf("  Total upstream→client bytes: %s\n", FormatBytesOrNA(u2c, ok)))

	// Policy
	b.WriteString("\n")
	b.WriteString(sectionTitle("Policy"))
	b.WriteString("\n")
	pAllow, ok := s.LabelValue("socks5_proxy_policy_decisions_total", "allow"), s.Has("socks5_proxy_policy_decisions_total")
	b.WriteString(fmt.Sprintf("  Policy allow: %s\n", FormatInt(pAllow, ok)))
	pDeny, _ := s.LabelValue("socks5_proxy_policy_decisions_total", "deny"), s.Has("socks5_proxy_policy_decisions_total")
	b.WriteString(fmt.Sprintf("  Policy deny: %s\n", FormatInt(pDeny, ok)))

	// Pipeline
	b.WriteString("\n")
	b.WriteString(sectionTitle("Analytics pipeline"))
	b.WriteString("\n")
	collected, ok := s.Scalar("pipeline_events_collected_total")
	b.WriteString(fmt.Sprintf("  Collected: %s\n", FormatInt(collected, ok && s.OK)))
	dropped, ok := s.LabelValue("pipeline_events_dropped_total", "collector"), s.Has("pipeline_events_dropped_total")
	b.WriteString(fmt.Sprintf("  Dropped (collector): %s\n", FormatInt(dropped, ok)))
	q, ok := s.LabelValue("pipeline_queue_events", "collector"), s.Has("pipeline_queue_events")
	b.WriteString(fmt.Sprintf("  Queue (collector): %s\n", FormatInt(q, ok)))
	normalized, ok := s.Scalar("pipeline_events_normalized_total")
	b.WriteString(fmt.Sprintf("  Normalized: %s\n", FormatInt(normalized, ok && s.OK)))
	published, ok := s.Scalar("pipeline_events_published_total")
	b.WriteString(fmt.Sprintf("  Published: %s\n", FormatInt(published, ok && s.OK)))
	pdrop, ok := s.Scalar("pipeline_events_publish_dropped_total")
	b.WriteString(fmt.Sprintf("  Publish dropped: %s\n", FormatInt(pdrop, ok && s.OK)))
	pfail, ok := s.Scalar("pipeline_publish_failures_total")
	b.WriteString(fmt.Sprintf("  Publish failures: %s\n", FormatInt(pfail, ok && s.OK)))

	// API aggregates
	b.WriteString("\n")
	b.WriteString(sectionTitle("Persisted traffic (last 24h, via API)"))
	b.WriteString("\n")
	if m.state.StatsErr != nil {
		b.WriteString(errStyle.Render("  " + SanitizeField(m.state.StatsErr.Error()) + "\n"))
	} else if m.state.Stats != nil {
		st := m.state.Stats
		b.WriteString(fmt.Sprintf("  Connections: %d\n", st.TotalConnections))
		b.WriteString(fmt.Sprintf("  Bytes client→upstream: %s\n", FormatBytes(float64(st.TotalBytesClientToUpstream))))
		b.WriteString(fmt.Sprintf("  Bytes upstream→client: %s\n", FormatBytes(float64(st.TotalBytesUpstreamToClient))))
		b.WriteString(fmt.Sprintf("  Avg dial latency: %.2f ms\n", st.AvgDialLatencyMs))
		b.WriteString(fmt.Sprintf("  Avg duration: %.2f ms\n", st.AvgDurationMs))
	} else {
		b.WriteString("  N/A\n")
	}

	// Freshness warning
	if m.isStale() {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render("  WARNING: metrics snapshot is stale"))
		b.WriteString("\n")
	}
	// Recent events
	b.WriteString("\n")
	b.WriteString(sectionTitle("Recent persisted connections"))
	b.WriteString("\n")
	logs := m.state.Logs
	if len(logs) == 0 {
		b.WriteString("  (none)\n")
	} else {
		n := 5
		if len(logs) < n {
			n = len(logs)
		}
		for i := 0; i < n; i++ {
			l := logs[i]
			b.WriteString(fmt.Sprintf("  %s  %s  %s:%d  %s  %s/%s\n",
				l.Timestamp.UTC().Format("15:04:05"),
				SanitizeField(MaskClientIP(l.ClientIP, m.cfg.RedactClientIPs)),
				SanitizeField(requestedHostOrIP(l)),
				l.DestinationPort,
				l.Outcome,
				FormatBytes(float64(l.BytesClientToUpstream)),
				FormatBytes(float64(l.BytesUpstreamToClient)),
			))
		}
	}
	return b.String()
}

func (m model) renderConnections() string {
	var b strings.Builder
	b.WriteString(sectionTitle("Connections (persisted history, newest first)"))
	b.WriteString("\n")
	logs := m.filteredLogs()
	if len(logs) == 0 {
		b.WriteString("  (no records)\n")
		return b.String()
	}
	header := fmt.Sprintf("  %-19s %-18s %-24s %-12s %10s %10s\n", "TIME", "CLIENT", "DESTINATION", "OUTCOME", "C→U", "U→C")
	b.WriteString(helpStyle.Render(header))
	for i, l := range logs {
		dest := l.RequestedHost
		if dest == "" {
			dest = l.DestinationIP
		}
		line := fmt.Sprintf("  %-19s %-18s %-24s %-12s %10s %10s",
			l.Timestamp.UTC().Format("2006-01-02 15:04:05"),
			Truncate(SanitizeField(MaskClientIP(l.ClientIP, m.cfg.RedactClientIPs)), 18),
			Truncate(SanitizeField(dest), 24),
			Truncate(SanitizeField(l.Outcome), 12),
			FormatBytes(float64(l.BytesClientToUpstream)),
			FormatBytes(float64(l.BytesUpstreamToClient)),
		)
		if i == m.selected {
			b.WriteString(activeTab.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m model) renderLogs() string {
	var b strings.Builder
	b.WriteString(sectionTitle("Logs / Events (persisted connection events)"))
	b.WriteString("\n")
	logs := m.filteredLogs()
	if len(logs) == 0 {
		b.WriteString("  (no events)\n")
		return b.String()
	}
	header := fmt.Sprintf("  %-19s %-8s %-12s %-24s %s\n", "TIME", "SEV", "OUTCOME", "DESTINATION", "MESSAGE")
	b.WriteString(helpStyle.Render(header))
	for i, l := range logs {
		dest := l.RequestedHost
		if dest == "" {
			dest = l.DestinationIP
		}
		sev := "info"
		if l.Outcome != "connected" {
			sev = "warn"
		}
		if l.Outcome == "auth_failed" || l.Outcome == "handshake_failed" {
			sev = "error"
		}
		msg := l.TerminationReason
		if l.PolicyReason != "" {
			msg += " (" + SanitizeField(l.PolicyReason) + ")"
		}
		line := fmt.Sprintf("  %-19s %-8s %-12s %-24s %s",
			l.Timestamp.UTC().Format("2006-01-02 15:04:05"),
			sev,
			Truncate(SanitizeField(l.Outcome), 12),
			Truncate(SanitizeField(dest), 24),
			SanitizeField(msg),
		)
		if i == m.selected {
			b.WriteString(activeTab.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m model) renderDiagnostics() string {
	var b strings.Builder
	s := m.state.Metrics
	b.WriteString(sectionTitle("Diagnostics (counters)"))
	b.WriteString("\n")
	b.WriteString("  Rejected by reason:\n")
	if m, ok := s.Vector("socks5_proxy_connections_rejected_total"); ok {
		for _, k := range SortedVectorKeys(m) {
			b.WriteString(fmt.Sprintf("    %-24s %s\n", k, FormatInt(m[k], true)))
		}
	} else {
		b.WriteString("    N/A\n")
	}
	b.WriteString("  Handshake failures by reason:\n")
	if m, ok := s.Vector("socks5_proxy_handshake_failures_total"); ok {
		for _, k := range SortedVectorKeys(m) {
			b.WriteString(fmt.Sprintf("    %-24s %s\n", k, FormatInt(m[k], true)))
		}
	} else {
		b.WriteString("    N/A\n")
	}
	b.WriteString("  Dial errors by reason:\n")
	if m, ok := s.Vector("socks5_proxy_dial_errors_total"); ok {
		for _, k := range SortedVectorKeys(m) {
			b.WriteString(fmt.Sprintf("    %-24s %s\n", k, FormatInt(m[k], true)))
		}
	} else {
		b.WriteString("    N/A\n")
	}
	b.WriteString("  Policy decisions:\n")
	if m, ok := s.Vector("socks5_proxy_policy_decisions_total"); ok {
		for _, k := range SortedVectorKeys(m) {
			b.WriteString(fmt.Sprintf("    %-24s %s\n", k, FormatInt(m[k], true)))
		}
	} else {
		b.WriteString("    N/A\n")
	}
	b.WriteString("\n")
	b.WriteString(sectionTitle("Top destinations (persisted)"))
	b.WriteString("\n")
	if len(m.state.TopDests) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for _, d := range m.state.TopDests {
			b.WriteString(fmt.Sprintf("  %-30s count=%d  c→u=%s  u→c=%s\n",
				Truncate(SanitizeField(d.Destination), 30),
				d.Count,
				FormatBytes(float64(d.TotalBytesClientToUpstream)),
				FormatBytes(float64(d.TotalBytesUpstreamToClient)),
			))
		}
	}
	b.WriteString("\n")
	b.WriteString(sectionTitle("Top client IPs (persisted)"))
	b.WriteString("\n")
	if len(m.state.TopClients) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for _, c := range m.state.TopClients {
			b.WriteString(fmt.Sprintf("  %-18s count=%d  c→u=%s  u→c=%s\n",
				Truncate(SanitizeField(MaskClientIP(c.ClientIP, m.cfg.RedactClientIPs)), 18),
				c.Count,
				FormatBytes(float64(c.TotalBytesClientToUpstream)),
				FormatBytes(float64(c.TotalBytesUpstreamToClient)),
			))
		}
	}
	// Recent failed connections
	b.WriteString("\n")
	b.WriteString(sectionTitle("Recent non-connected persisted events"))
	b.WriteString("\n")
	var failed []models.TrafficLog
	for _, l := range m.state.Logs {
		if l.Outcome != "connected" {
			failed = append(failed, l)
		}
	}
	if len(failed) == 0 {
		b.WriteString("  (none)\n")
	} else {
		n := 10
		if len(failed) < n {
			n = len(failed)
		}
		for i := 0; i < n; i++ {
			l := failed[i]
			b.WriteString(fmt.Sprintf("  %s  %s  %s  %s\n",
				l.Timestamp.UTC().Format("15:04:05"),
				SanitizeField(MaskClientIP(l.ClientIP, m.cfg.RedactClientIPs)),
				l.Outcome,
				SanitizeField(l.TerminationReason),
			))
		}
	}
	return b.String()
}

func (m model) renderDetail(l models.TrafficLog) string {
	var b strings.Builder
	b.WriteString(sectionTitle("Connection detail"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  Event ID: %s\n", SanitizeField(l.EventID)))
	b.WriteString(fmt.Sprintf("  Client: %s:%d\n", SanitizeField(MaskClientIP(l.ClientIP, m.cfg.RedactClientIPs)), l.ClientPort))
	b.WriteString(fmt.Sprintf("  Requested host: %s\n", SanitizeField(l.RequestedHost)))
	b.WriteString(fmt.Sprintf("  Destination IP: %s\n", SanitizeField(l.DestinationIP)))
	b.WriteString(fmt.Sprintf("  Destination port: %d\n", l.DestinationPort))
	b.WriteString(fmt.Sprintf("  Outcome: %s\n", SanitizeField(l.Outcome)))
	b.WriteString(fmt.Sprintf("  Termination: %s\n", SanitizeField(l.TerminationReason)))
	b.WriteString(fmt.Sprintf("  Workload: %s\n", SanitizeField(l.WorkloadID)))
	b.WriteString(fmt.Sprintf("  Policy: %s (%s)\n", SanitizeField(l.PolicyDecision), SanitizeField(l.PolicyReason)))
	b.WriteString(fmt.Sprintf("  Policy ID/version: %s / %s\n", SanitizeField(l.PolicyID), SanitizeField(l.PolicyVersion)))
	b.WriteString(fmt.Sprintf("  Started: %s\n", l.Timestamp.UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("  Duration: %d ms\n", l.DurationMs))
	b.WriteString(fmt.Sprintf("  Dial latency: %d ms\n", l.DialLatencyMs))
	b.WriteString(fmt.Sprintf("  Bytes client→upstream: %d\n", l.BytesClientToUpstream))
	b.WriteString(fmt.Sprintf("  Bytes upstream→client: %d\n", l.BytesUpstreamToClient))
	b.WriteString(fmt.Sprintf("  Schema version: %d\n", l.SchemaVersion))
	b.WriteString(helpStyle.Render("  (Esc/Enter to close)"))
	return b.String()
}

func (m model) renderHelp() string {
	return sectionTitle("Help") + "\n" +
		"  1-4: switch views\n" +
		"  j/k or arrows: navigate\n" +
		"  /: filter text\n" +
		"  Enter: inspect selected connection\n" +
		"  p: pause/resume auto-refresh\n" +
		"  r: refresh now\n" +
		"  ?: toggle help\n" +
		"  q: quit\n" +
		"  Values come from the proxy Prometheus endpoint and the analytics API.\n" +
		"  N/A means the metric or data source is unavailable, not zero."
}

func sectionTitle(s string) string {
	return titleStyle.Render(s)
}

func requestedHostOrIP(l models.TrafficLog) string {
	if l.RequestedHost != "" {
		return l.RequestedHost
	}
	return l.DestinationIP
}

func FormatBytesOrNA(v float64, ok bool) string {
	if !ok {
		return "N/A"
	}
	return FormatBytes(v)
}


