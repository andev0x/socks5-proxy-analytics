// Package tui implements the terminal monitoring interface for SOCKS5 Proxy Analytics.
package tui

import (
	"fmt"
	"strings"
)

// SanitizeField removes control characters and terminal escape sequences from a
// value that originated outside the process (hostnames, IPs, outcomes, ...).
// The result is safe to render in a terminal and never contains newlines.
func SanitizeField(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteRune(' ')
		case r == '\n' || r == '\r':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			continue
		case r >= 0x80 && r <= 0x9f:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SanitizeTerminal keeps newlines for multi-line rendering but strips other
// control and C1 control characters.
func SanitizeTerminal(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		if r >= 0x80 && r <= 0x9f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Truncate shortens s to at most n runes, appending an ellipsis when truncated.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// MaskClientIP optionally masks the last octet of an IPv4 address or the last
// group of an IPv6 address. It is a presentation-time control and never changes
// stored data.
func MaskClientIP(ip string, enabled bool) string {
	ip = SanitizeField(ip)
	if !enabled || ip == "" {
		return ip
	}
	if strings.Contains(ip, ":") {
		parts := strings.Split(ip, ":")
		if len(parts) > 1 {
			parts[len(parts)-1] = "xxxx"
			return strings.Join(parts, ":")
		}
		return ip
	}
	parts := strings.Split(ip, ".")
	if len(parts) == 4 {
		parts[3] = "xxx"
		return strings.Join(parts, ".")
	}
	return ip
}

// FormatBytes renders a byte count in a human readable form.
func FormatBytes(v float64) string {
	if v < 0 {
		return "N/A"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.2f %s", v, units[i])
}

// FormatRate renders bytes/second or N/A when the rate is not valid.
func FormatRate(v float64, valid bool) string {
	if !valid {
		return "N/A"
	}
	return FormatBytes(v) + "/s"
}

// FormatInt renders an integer-valued metric, or N/A when unavailable.
func FormatInt(v float64, ok bool) string {
	if !ok {
		return "N/A"
	}
	return fmt.Sprintf("%.0f", v)
}

// FormatFloat renders a float metric with two decimals, or N/A when unavailable.
func FormatFloat(v float64, ok bool) string {
	if !ok {
		return "N/A"
	}
	return fmt.Sprintf("%.2f", v)
}
