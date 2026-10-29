package policy

import (
	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/security"
)

// NewEngineFromConfig builds a policy engine from application configuration.
func NewEngineFromConfig(cfg *config.Config) (*Engine, error) {
	engine := NewEngine()

	if !cfg.Policy.Enabled {
		return engine, nil
	}

	policy := &Policy{
		ID:              cfg.Policy.ID,
		Version:         cfg.Policy.Version,
		DefaultAction:   Action(cfg.Policy.DefaultAction),
		RestrictedCIDRs: append([]string(nil), cfg.Policy.RestrictedCIDRs...),
	}

	for i := range cfg.Policy.Rules {
		rule := &cfg.Policy.Rules[i]
		policy.Rules = append(policy.Rules, Rule{
			ID:         rule.ID,
			Workload:   rule.Workload,
			Action:     Action(rule.Action),
			Host:       rule.Host,
			HostSuffix: rule.HostSuffix,
			IP:         rule.IP,
			Port:       rule.Port,
			PortStart:  rule.PortStart,
			PortEnd:    rule.PortEnd,
			Priority:   rule.Priority,
			Index:      i,
		})
	}

	if err := engine.SetPolicy(policy); err != nil {
		return nil, err
	}

	return engine, nil
}

// WorkloadCredentialsFromConfig converts configured workloads to security credentials.
func WorkloadCredentialsFromConfig(cfg *config.Config) []security.WorkloadCredential {
	result := make([]security.WorkloadCredential, 0, len(cfg.Proxy.Workloads))

	for _, workload := range cfg.Proxy.Workloads {
		result = append(result, security.WorkloadCredential{
			WorkloadID: workload.ID,
			Username:   workload.Username,
			Password:   workload.Password,
		})
	}

	return result
}
