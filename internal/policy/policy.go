// Package policy implements a small deterministic egress policy engine.
package policy

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Action is a policy decision outcome.
type Action string

const (
	// ActionAllow permits the connection.
	ActionAllow Action = "allow"
	// ActionDeny rejects the connection.
	ActionDeny Action = "deny"
)

// Request is the normalized authorization request evaluated by the engine.
type Request struct {
	Workload string
	Host     string
	IP       string
	Port     int
}

// Rule is a single declarative policy rule.
type Rule struct {
	ID         string
	Workload   string
	Action     Action
	Host       string
	HostSuffix string
	IP         string
	Port       int
	PortStart  int
	PortEnd    int
	Priority   int
	Index      int
}

// Policy is an immutable, validated policy snapshot.
type Policy struct {
	ID              string
	Version         string
	DefaultAction   Action
	Rules           []Rule
	RestrictedCIDRs []string
	restrictedNets  []*net.IPNet
}

// Decision is the result of evaluating a request.
type Decision struct {
	Allowed       bool
	Reason        string
	PolicyID      string
	PolicyVersion string
	Workload      string
	Host          string
	IP            string
	Port          int
	Timestamp     time.Time
}

// Engine holds the active policy snapshot atomically.
type Engine struct {
	mu       sync.RWMutex
	snapshot *Policy
}

// NewEngine creates an empty engine (fail-closed until a policy is set).
func NewEngine() *Engine {
	return &Engine{}
}

// SetPolicy validates and activates a policy snapshot atomically.
func (e *Engine) SetPolicy(p *Policy) error {
	if p == nil {
		return errors.New("policy is nil")
	}

	if err := p.Validate(); err != nil {
		return err
	}

	p.prepareRestricted()

	e.mu.Lock()
	defer e.mu.Unlock()

	e.snapshot = p

	return nil
}

// Policy returns the active snapshot (may be nil).
func (e *Engine) Policy() *Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.snapshot
}

// Evaluate evaluates a request against the active policy. Fail-closed on missing policy.
func (e *Engine) Evaluate(req Request) Decision {
	now := time.Now().UTC()

	e.mu.RLock()
	p := e.snapshot
	e.mu.RUnlock()

	decision := Decision{
		PolicyID:      "",
		PolicyVersion: "",
		Workload:      req.Workload,
		Host:          req.Host,
		IP:            req.IP,
		Port:          req.Port,
		Timestamp:     now,
	}

	if p == nil {
		decision.Allowed = false
		decision.Reason = "policy_unavailable"
		decision.PolicyID = "none"
		decision.PolicyVersion = "none"

		return decision
	}

	decision.PolicyID = p.ID
	decision.PolicyVersion = p.Version

	if req.Workload == "" {
		decision.Allowed = false
		decision.Reason = "missing_identity"

		return decision
	}

	// Collect matching rules.
	denyRule, allowRule := selectRules(p.Rules, req)

	if denyRule != nil {
		decision.Allowed = false
		decision.Reason = denyReason(denyRule)

		return decision
	}

	if allowRule == nil {
		decision.Allowed = false
		decision.Reason = "default_deny"

		return decision
	}

	// Restricted destination check: an allow rule must explicitly cover the IP.
	if p.isRestricted(req.IP) && (allowRule.IP == "" || !ipContains(allowRule.IP, req.IP)) {
		decision.Allowed = false
		decision.Reason = "restricted_destination"

		return decision
	}

	decision.Allowed = true
	decision.Reason = "allowed"

	return decision
}

func denyReason(rule *Rule) string {
	if rule.ID != "" {
		return "deny_rule:" + rule.ID
	}

	return "deny_rule"
}

func selectRules(rules []Rule, req Request) (denyRule, allowRule *Rule) {
	for i := range rules {
		rule := &rules[i]
		if !rule.matches(req) {
			continue
		}

		if rule.Action == ActionDeny {
			if denyRule == nil || higherPriority(rule, denyRule) {
				ruleCopy := *rule
				denyRule = &ruleCopy
			}

			continue
		}

		if allowRule == nil || higherPriority(rule, allowRule) {
			ruleCopy := *rule
			allowRule = &ruleCopy
		}
	}

	return denyRule, allowRule
}

func higherPriority(candidate, current *Rule) bool {
	if candidate.Priority != current.Priority {
		return candidate.Priority > current.Priority
	}

	return candidate.Index < current.Index
}

func (r *Rule) matches(req Request) bool {
	if !workloadMatches(r.Workload, req.Workload) {
		return false
	}

	if !hostMatches(r, req.Host) {
		return false
	}

	if r.IP != "" && !ipMatches(r.IP, req.IP) {
		return false
	}

	return portMatches(r, req.Port)
}

func workloadMatches(ruleWorkload, reqWorkload string) bool {
	if ruleWorkload == "" || ruleWorkload == "*" {
		return true
	}

	return strings.EqualFold(ruleWorkload, reqWorkload)
}

func hostMatches(r *Rule, host string) bool {
	if r.Host == "" && r.HostSuffix == "" {
		return true
	}

	if r.Host != "" && strings.EqualFold(r.Host, host) {
		return true
	}

	if r.HostSuffix != "" {
		suffix := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(r.HostSuffix), "."))
		normalized := strings.ToLower(host)
		if normalized == suffix || strings.HasSuffix(normalized, "."+suffix) {
			return true
		}
	}

	return false
}

func portMatches(r *Rule, port int) bool {
	if r.Port == 0 && r.PortStart == 0 && r.PortEnd == 0 {
		return true
	}

	if r.Port != 0 && port == r.Port {
		return true
	}

	if r.PortStart != 0 || r.PortEnd != 0 {
		start := r.PortStart
		end := r.PortEnd
		if start == 0 {
			start = 1
		}
		if end == 0 {
			end = 65535
		}
		if port >= start && port <= end {
			return true
		}
	}

	return false
}

// Validate validates a policy before activation.
func (p *Policy) Validate() error {
	if p.DefaultAction == "" {
		p.DefaultAction = ActionDeny
	}

	if p.DefaultAction != ActionDeny {
		return errors.New("policy default action must be deny")
	}

	if p.ID == "" {
		p.ID = "default"
	}

	if p.Version == "" {
		p.Version = "1"
	}

	for i := range p.Rules {
		rule := &p.Rules[i]
		if err := validateRule(rule, i); err != nil {
			return err
		}

		rule.Index = i
	}

	for _, cidr := range p.RestrictedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			if net.ParseIP(cidr) == nil {
				return fmt.Errorf("invalid restricted cidr %q", cidr)
			}
		}
	}

	return nil
}

func validateRule(rule *Rule, index int) error {
	if rule.Action != ActionAllow && rule.Action != ActionDeny {
		return fmt.Errorf("rule %d has invalid action %q", index, rule.Action)
	}

	if rule.Workload == "" {
		rule.Workload = "*"
	}

	if rule.Host != "" {
		rule.Host = strings.ToLower(strings.TrimSpace(rule.Host))
	}

	if rule.HostSuffix != "" {
		rule.HostSuffix = strings.ToLower(strings.TrimSpace(rule.HostSuffix))
	}

	if rule.IP != "" {
		if err := normalizeRuleIP(rule, index); err != nil {
			return err
		}
	}

	return nil
}

func normalizeRuleIP(rule *Rule, index int) error {
	rule.IP = canonicalIPSpec(rule.IP)

	if _, _, err := net.ParseCIDR(rule.IP); err == nil {
		return nil
	}

	parsed := net.ParseIP(rule.IP)
	if parsed == nil {
		return fmt.Errorf("rule %d has invalid ip %q", index, rule.IP)
	}

	bits := 32
	if parsed.To4() == nil {
		bits = 128
	}

	rule.IP = parsed.String() + "/" + strconv.Itoa(bits)

	return nil
}

func (p *Policy) prepareRestricted() {
	if len(p.RestrictedCIDRs) == 0 {
		p.RestrictedCIDRs = []string{
			"127.0.0.0/8",
			"::1/128",
			"169.254.0.0/16",
			"fe80::/10",
			"0.0.0.0/8",
			"::/128",
		}
	}

	p.restrictedNets = nil
	for _, cidr := range p.RestrictedCIDRs {
		cidr = canonicalIPSpec(cidr)
		if ip := net.ParseIP(cidr); ip != nil {
			if ip.To4() != nil {
				cidr = ip.String() + "/32"
			} else {
				cidr = ip.String() + "/128"
			}
		}

		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}

		p.restrictedNets = append(p.restrictedNets, network)
	}
}

func (p *Policy) isRestricted(ip string) bool {
	parsed := net.ParseIP(canonicalIP(ip))
	if parsed == nil {
		return false
	}

	for _, network := range p.restrictedNets {
		if network.Contains(parsed) {
			return true
		}
	}

	return false
}

func ipMatches(spec, ip string) bool {
	if spec == "" {
		return true
	}

	parsedIP := net.ParseIP(canonicalIP(ip))
	if parsedIP == nil {
		return false
	}

	spec = canonicalIPSpec(spec)
	if strings.Contains(spec, "/") {
		_, network, err := net.ParseCIDR(spec)
		if err != nil {
			return false
		}

		return network.Contains(parsedIP)
	}

	parsedSpec := net.ParseIP(spec)
	if parsedSpec == nil {
		return false
	}

	return parsedSpec.Equal(parsedIP)
}

func ipContains(spec, ip string) bool {
	return ipMatches(spec, ip)
}

// SortRules sorts rules deterministically (exported for tests).
func SortRules(rules []Rule) {
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority > rules[j].Priority
		}

		if rules[i].Action != rules[j].Action {
			return rules[i].Action == ActionDeny
		}

		return rules[i].Index < rules[j].Index
	})
}

// canonicalIP returns the canonical textual form of an IP address. IPv4-mapped IPv6
// addresses are canonicalized to their IPv4 form so policy rules and restricted ranges
// cannot be bypassed by an alternate address representation.
func canonicalIP(value string) string {
	value = strings.TrimSpace(value)
	ip := net.ParseIP(value)
	if ip == nil {
		return value
	}

	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}

	return ip.String()
}

// canonicalIPSpec applies canonicalIP to the host part of an IP or CIDR specification.
func canonicalIPSpec(value string) string {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "/") {
		return canonicalIP(value)
	}

	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 {
		return value
	}

	return canonicalIP(parts[0]) + "/" + parts[1]
}
