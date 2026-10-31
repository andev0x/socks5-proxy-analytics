package tui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/andev0x/socks5-proxy-analytics/internal/models"
)

// view identifiers.
const (
	viewOverview    = 0
	viewConnections = 1
	viewLogs        = 2
	viewDiagnostics = 3
)

type tickMsg time.Time

type dataMsg struct {
	state DataSourceState
	prev  *MetricsSnapshot
	rates RateState
}

type model struct {
	cfg    Config
	client *http.Client
	api    *APIClient

	width   int
	height  int
	view    int
	paused  bool
	loading bool

	state DataSourceState
	prev  *MetricsSnapshot
	rates RateState

	selected    int
	filter      string
	filtering   bool
	filterInput textinput.Model

	detail   *models.TrafficLog
	showHelp bool

	lastFetch  time.Time
	staleAfter time.Duration
}

// Model is the exported name of the TUI model.
type Model = model

// NewModel creates the TUI model.
func NewModel(cfg Config) model {
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 2 * time.Second
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 5 * time.Second
	}
	if cfg.LogLimit <= 0 {
		cfg.LogLimit = 100
	}
	ti := textinput.New()
	ti.Placeholder = "filter..."
	ti.CharLimit = 128
	m := model{
		cfg:         cfg,
		client:      &http.Client{Timeout: cfg.HTTPTimeout},
		view:        viewOverview,
		loading:     true,
		filterInput: ti,
		staleAfter:  3 * cfg.RefreshInterval,
	}
	m.api = NewAPIClient(cfg.APIURL, m.client)
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.fetchCmd(), tickCmd(m.cfg.RefreshInterval))
}

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) fetchCmd() tea.Cmd {
	cfg := m.cfg
	client := m.client
	api := m.api
	prev := m.prev
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.HTTPTimeout)
		defer cancel()
		st := collectState(ctx, cfg, client, api)
		rates := RateState{}
		if prev != nil && prev.OK && st.Metrics.OK {
			dt := st.Metrics.ScrapedAt.Sub(prev.ScrapedAt).Seconds()
			if dt > 0 {
				dc := st.Metrics.LabelValue("socks5_proxy_relay_bytes_total", "client_to_upstream") -
					prev.LabelValue("socks5_proxy_relay_bytes_total", "client_to_upstream")
				du := st.Metrics.LabelValue("socks5_proxy_relay_bytes_total", "upstream_to_client") -
					prev.LabelValue("socks5_proxy_relay_bytes_total", "upstream_to_client")
				if dc >= 0 && du >= 0 {
					rates.ClientToUpstreamPerSec = dc / dt
					rates.UpstreamToClientPerSec = du / dt
					rates.Valid = true
				}
			}
		}
		snapCopy := st.Metrics
		return dataMsg{state: st, prev: &snapCopy, rates: rates}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.filtering {
			switch msg.Type {
			case tea.KeyEnter:
				m.filter = m.filterInput.Value()
				m.filtering = false
				m.selected = 0
				return m, nil
			case tea.KeyEsc:
				m.filtering = false
				m.filterInput.SetValue(m.filter)
				return m, nil
			}
			var cmd tea.Cmd
			m.filterInput, cmd = m.filterInput.Update(msg)
			return m, cmd
		}
		if m.detail != nil {
			switch msg.String() {
			case "esc", "q", "enter":
				m.detail = nil
			}
			return m, nil
		}
		if m.showHelp {
			switch msg.String() {
			case "esc", "q", "?":
				m.showHelp = false
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			m.showHelp = true
		case "p":
			m.paused = !m.paused
		case "r":
			m.loading = true
			return m, m.fetchCmd()
		case "1":
			m.view = viewOverview
			m.selected = 0
		case "2":
			m.view = viewConnections
			m.selected = 0
		case "3":
			m.view = viewLogs
			m.selected = 0
		case "4":
			m.view = viewDiagnostics
			m.selected = 0
		case "/":
			m.filtering = true
			m.filterInput.SetValue(m.filter)
			m.filterInput.Focus()
			return m, textinput.Blink
		case "j", "down":
			m.moveSelection(1)
		case "k", "up":
			m.moveSelection(-1)
		case "enter":
			m.openDetail()
		}
		return m, nil
	case tickMsg:
		if m.paused {
			return m, tickCmd(m.cfg.RefreshInterval)
		}
		m.loading = true
		return m, tea.Batch(m.fetchCmd(), tickCmd(m.cfg.RefreshInterval))
	case dataMsg:
		m.loading = false
		m.state = msg.state
		m.prev = msg.prev
		m.rates = msg.rates
		m.lastFetch = time.Now().UTC()
		m.clampSelection()
		return m, nil
	}
	return m, nil
}

func (m *model) moveSelection(delta int) {
	n := m.currentListLen()
	if n == 0 {
		m.selected = 0
		return
	}
	m.selected += delta
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= n {
		m.selected = n - 1
	}
}

func (m *model) clampSelection() {
	n := m.currentListLen()
	if n == 0 {
		m.selected = 0
		return
	}
	if m.selected >= n {
		m.selected = n - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

func (m model) currentListLen() int {
	switch m.view {
	case viewConnections, viewLogs:
		return len(m.filteredLogs())
	case viewDiagnostics:
		return len(m.state.TopDests)
	}
	return 0
}

func (m *model) openDetail() {
	if m.view != viewConnections && m.view != viewLogs {
		return
	}
	logs := m.filteredLogs()
	if m.selected < 0 || m.selected >= len(logs) {
		return
	}
	l := logs[m.selected]
	m.detail = &l
}

func (m model) filteredLogs() []models.TrafficLog {
	if m.filter == "" {
		return m.state.Logs
	}
	q := strings.ToLower(m.filter)
	var out []models.TrafficLog
	for _, l := range m.state.Logs {
		hay := strings.ToLower(strings.Join([]string{
			SanitizeField(l.ClientIP),
			SanitizeField(l.RequestedHost),
			SanitizeField(l.DestinationIP),
			l.Outcome,
			l.TerminationReason,
		}, " "))
		if strings.Contains(hay, q) {
			out = append(out, l)
		}
	}
	return out
}

func (m model) isStale() bool {
	if !m.state.Metrics.OK {
		return false
	}
	return time.Since(m.state.Metrics.ScrapedAt) > m.staleAfter
}

// lipgloss styles.
var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	tabStyle   = lipgloss.NewStyle().Padding(0, 1)
	activeTab  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("63")).Padding(0, 1)
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
)

func (m model) View() string {
	if m.width == 0 {
		return "Loading TUI..."
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("SOCKS5 Proxy Analytics — Monitor"))
	b.WriteString("  ")
	if m.loading {
		b.WriteString(helpStyle.Render("refreshing…"))
	}
	b.WriteString("\n")
	b.WriteString(m.renderTabs())
	b.WriteString("\n")
	b.WriteString(m.renderStatusLine())
	b.WriteString("\n\n")

	switch m.view {
	case viewOverview:
		b.WriteString(m.renderOverview())
	case viewConnections:
		b.WriteString(m.renderConnections())
	case viewLogs:
		b.WriteString(m.renderLogs())
	case viewDiagnostics:
		b.WriteString(m.renderDiagnostics())
	}

	if m.filtering {
		b.WriteString("\n")
		b.WriteString("Filter: " + m.filterInput.View())
	}
	if m.detail != nil {
		b.WriteString("\n\n")
		b.WriteString(m.renderDetail(*m.detail))
	}
	if m.showHelp {
		b.WriteString("\n\n")
		b.WriteString(m.renderHelp())
	}
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("keys: 1-4 views  / filter  j/k navigate  enter detail  p pause  r refresh  ? help  q quit"))
	if m.paused {
		b.WriteString(helpStyle.Render("   [PAUSED]"))
	}
	return b.String()
}

func (m model) renderTabs() string {
	names := []string{"Overview", "Connections", "Logs/Events", "Diagnostics"}
	parts := make([]string, 0, len(names))
	for i, name := range names {
		if i == m.view {
			parts = append(parts, activeTab.Render(name))
		} else {
			parts = append(parts, tabStyle.Render(name))
		}
	}
	return strings.Join(parts, " ")
}

func (m model) renderStatusLine() string {
	var parts []string
	if m.state.Metrics.OK {
		age := time.Since(m.state.Metrics.ScrapedAt).Round(time.Second)
		status := okStyle.Render("metrics: OK")
		if m.isStale() {
			status = warnStyle.Render("metrics: STALE")
		}
		parts = append(parts, fmt.Sprintf("%s (%s ago)", status, age))
	} else {
		msg := m.state.Metrics.Err
		if msg == "" {
			msg = "unavailable"
		}
		parts = append(parts, errStyle.Render("metrics: "+SanitizeField(msg)))
	}
	if m.state.HealthErr == nil && !m.state.APIAt.IsZero() {
		age := time.Since(m.state.APIAt).Round(time.Second)
		parts = append(parts, okStyle.Render(fmt.Sprintf("api: OK (%s ago)", age)))
	} else {
		msg := "unavailable"
		if m.state.APIErr != nil {
			msg = m.state.APIErr.Error()
		} else if m.state.HealthErr != nil {
			msg = m.state.HealthErr.Error()
		}
		parts = append(parts, errStyle.Render("api: "+SanitizeField(msg)))
	}
	if m.state.StatsErr != nil {
		parts = append(parts, warnStyle.Render("stats: "+SanitizeField(m.state.StatsErr.Error())))
	}
	if m.state.LogsErr != nil {
		parts = append(parts, warnStyle.Render("logs: "+SanitizeField(m.state.LogsErr.Error())))
	}
	return strings.Join(parts, "  |  ")
}
