package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/andev0x/socks5-proxy-analytics/internal/tui"
)

func main() {
	metricsURL := flag.String("metrics-url", envOr("METRICS_URL", "http://127.0.0.1:9090/metrics"), "Prometheus metrics endpoint")
	apiURL := flag.String("api-url", envOr("API_URL", "http://127.0.0.1:8080"), "Analytics API base URL")
	refresh := flag.Duration("refresh", 2*time.Second, "Auto-refresh interval")
	timeout := flag.Duration("timeout", 5*time.Second, "HTTP request timeout")
	limit := flag.Int("limit", 100, "Number of persisted logs to load")
	redact := flag.Bool("redact-client-ips", false, "Mask client IP addresses in the UI")
	once := flag.Bool("once", false, "Collect one snapshot, print it, and exit")
	jsonOut := flag.Bool("json", false, "With -once, print JSON instead of text")
	flag.Parse()

	cfg := tui.Config{
		MetricsURL:      *metricsURL,
		APIURL:          *apiURL,
		RefreshInterval: *refresh,
		HTTPTimeout:     *timeout,
		LogLimit:        *limit,
		RedactClientIPs: *redact,
	}

	if *once {
		st, _, err := tui.FetchOnce(cfg)
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(st); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if err != nil {
				os.Exit(1)
			}
			return
		}
		fmt.Printf("metrics OK: %v\n", st.Metrics.OK)
		if st.Metrics.Err != "" {
			fmt.Printf("metrics error: %s\n", st.Metrics.Err)
		}
		if st.APIErr != nil {
			fmt.Printf("api error: %s\n", st.APIErr)
		}
		if st.Stats != nil {
			fmt.Printf("connections(24h): %d  c->u: %d bytes  u->c: %d bytes\n",
				st.Stats.TotalConnections, st.Stats.TotalBytesClientToUpstream, st.Stats.TotalBytesUpstreamToClient)
		}
		fmt.Printf("logs: %d\n", len(st.Logs))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	m := tui.NewModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "TUI error:", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
