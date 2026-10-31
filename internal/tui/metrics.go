package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	prommodel "github.com/prometheus/common/model"

	"github.com/prometheus/common/expfmt"
)

// MetricsSnapshot is a point-in-time scrape of the proxy Prometheus endpoint.
type MetricsSnapshot struct {
	ScrapedAt  time.Time
	OK         bool
	Err        string
	Present    map[string]bool
	Scalars    map[string]float64
	Vectors    map[string]map[string]float64
	Histograms map[string]struct {
		Count uint64
		Sum   float64
	}
}

// NewMetricsSnapshot creates an empty snapshot.
func NewMetricsSnapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Present: make(map[string]bool),
		Scalars: make(map[string]float64),
		Vectors: make(map[string]map[string]float64),
		Histograms: make(map[string]struct {
			Count uint64
			Sum   float64
		}),
	}
}

// Scalar returns a scalar metric and whether it was present.
func (s MetricsSnapshot) Scalar(name string) (float64, bool) {
	v, ok := s.Scalars[name]
	return v, ok && s.Present[name]
}

// Vector returns a labeled metric map and whether the family was present.
func (s MetricsSnapshot) Vector(name string) (map[string]float64, bool) {
	v, ok := s.Vectors[name]
	return v, ok && s.Present[name]
}

// LabelValue returns one labeled value, defaulting to zero when absent.
func (s MetricsSnapshot) LabelValue(name, label string) float64 {
	if m, ok := s.Vectors[name]; ok {
		return m[label]
	}
	return 0
}

// Has reports whether a metric family was present in the scrape.
func (s MetricsSnapshot) Has(name string) bool {
	return s.Present[name]
}

// HistCount returns histogram sample count.
func (s MetricsSnapshot) HistCount(name string) (uint64, bool) {
	h, ok := s.Histograms[name]
	return h.Count, ok && s.Present[name]
}

// HistSum returns histogram sample sum.
func (s MetricsSnapshot) HistSum(name string) (float64, bool) {
	h, ok := s.Histograms[name]
	return h.Sum, ok && s.Present[name]
}

// VectorSum returns the sum of all labeled values for a metric.
func (s MetricsSnapshot) VectorSum(name string) (float64, bool) {
	m, ok := s.Vector(name)
	if !ok {
		return 0, false
	}
	var total float64
	for _, v := range m {
		total += v
	}
	return total, true
}

// ScrapeMetrics fetches and parses a Prometheus text endpoint.
func ScrapeMetrics(ctx context.Context, client *http.Client, url string) MetricsSnapshot {
	snap := NewMetricsSnapshot()
	snap.ScrapedAt = time.Now().UTC()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		snap.Err = err.Error()
		return snap
	}
	resp, err := client.Do(req)
	if err != nil {
		snap.Err = err.Error()
		return snap
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		snap.Err = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
		return snap
	}

	parser := expfmt.NewTextParser(prommodel.UTF8Validation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		snap.Err = err.Error()
		return snap
	}

	for name, fam := range families {
		snap.Present[name] = true
		for _, m := range fam.GetMetric() {
			switch fam.GetType() {
			case dto.MetricType_HISTOGRAM:
				h := m.GetHistogram()
				snap.Histograms[name] = struct {
					Count uint64
					Sum   float64
				}{Count: h.GetSampleCount(), Sum: h.GetSampleSum()}
			default:
				val := 0.0
				if c := m.GetCounter(); c != nil {
					val = c.GetValue()
				} else if g := m.GetGauge(); g != nil {
					val = g.GetValue()
				} else if u := m.GetUntyped(); u != nil {
					val = u.GetValue()
				}
				labels := m.GetLabel()
				if len(labels) == 0 {
					snap.Scalars[name] = val
					continue
				}
				if snap.Vectors[name] == nil {
					snap.Vectors[name] = make(map[string]float64)
				}
				key := labelKey(labels)
				snap.Vectors[name][key] = val
			}
		}
	}
	snap.OK = true
	return snap
}

func labelKey(labels []*dto.LabelPair) string {
	if len(labels) == 1 {
		return labels[0].GetValue()
	}
	parts := make([]string, 0, len(labels))
	for _, lp := range labels {
		parts = append(parts, lp.GetName()+"="+lp.GetValue())
	}
	return strings.Join(parts, ",")
}

// SortedVectorKeys returns sorted keys for deterministic rendering.
func SortedVectorKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
