package tui

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/models"
)

// Config configures the TUI data sources.
type Config struct {
	MetricsURL      string
	APIURL          string
	RefreshInterval time.Duration
	HTTPTimeout     time.Duration
	LogLimit        int
	RedactClientIPs bool
}

// DataSourceState is the latest collected data from all sources.
type DataSourceState struct {
	Metrics    MetricsSnapshot
	MetricsAt  time.Time
	APIErr     error
	APIAt      time.Time
	HealthErr  error
	HealthAt   time.Time
	Stats      *models.TrafficStats
	StatsErr   error
	StatsAt    time.Time
	Logs       []models.TrafficLog
	LogsErr    error
	LogsAt     time.Time
	TopDests   []models.DestinationStats
	TopClients []models.ClientIPStats
	AggErr     error
}

// RateState holds derived throughput rates between two scrapes.
type RateState struct {
	ClientToUpstreamPerSec float64
	UpstreamToClientPerSec float64
	Valid                  bool
}

// FetchOnce collects a single snapshot without starting the TUI.
func FetchOnce(cfg Config) (DataSourceState, RateState, error) {
	client := &http.Client{Timeout: cfg.HTTPTimeout}
	api := NewAPIClient(cfg.APIURL, client)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.HTTPTimeout)
	defer cancel()
	st := collectState(ctx, cfg, client, api)
	rates := RateState{}
	// No previous sample available for a one-shot fetch.
	return st, rates, firstError(st)
}

func collectState(ctx context.Context, cfg Config, client *http.Client, api *APIClient) DataSourceState {
	st := DataSourceState{}
	snap := ScrapeMetrics(ctx, client, cfg.MetricsURL)
	st.Metrics = snap
	st.MetricsAt = snap.ScrapedAt

	if err := api.Health(ctx); err != nil {
		st.HealthErr = err
	} else {
		st.HealthAt = time.Now().UTC()
		st.APIAt = st.HealthAt
	}
	if err := api.Livez(ctx); err != nil {
		if st.APIErr == nil {
			st.APIErr = err
		}
	} else if st.APIAt.IsZero() {
		st.APIAt = time.Now().UTC()
	}

	now := time.Now().UTC()
	stats, err := api.TrafficStats(ctx, now.Add(-24*time.Hour), now)
	if err != nil {
		st.StatsErr = err
	} else {
		st.Stats = stats
		st.StatsAt = now
		if st.APIAt.IsZero() {
			st.APIAt = now
		}
	}
	logs, err := api.TrafficLogs(ctx, cfg.LogLimit, 0, now.Add(-24*time.Hour), now)
	if err != nil {
		st.LogsErr = err
	} else {
		st.Logs = logs
		st.LogsAt = now
		if st.APIAt.IsZero() {
			st.APIAt = now
		}
	}
	dests, err := api.TopDestinations(ctx, 10)
	if err != nil {
		if st.AggErr == nil {
			st.AggErr = err
		}
	} else {
		st.TopDests = dests
	}
	clients, err := api.TopClientIPs(ctx, 10)
	if err != nil {
		if st.AggErr == nil {
			st.AggErr = err
		}
	} else {
		st.TopClients = clients
	}
	if st.APIErr == nil && (st.HealthErr != nil || st.StatsErr != nil || st.LogsErr != nil) {
		// Keep a single representative API error for the header.
		switch {
		case st.HealthErr != nil:
			st.APIErr = st.HealthErr
		case st.StatsErr != nil:
			st.APIErr = st.StatsErr
		default:
			st.APIErr = st.LogsErr
		}
	}
	return st
}

func firstError(st DataSourceState) error {
	if !st.Metrics.OK {
		if st.Metrics.Err != "" {
			return fmt.Errorf("metrics: %s", st.Metrics.Err)
		}
		return fmt.Errorf("metrics: scrape failed")
	}
	if st.APIErr != nil {
		return st.APIErr
	}
	return nil
}
