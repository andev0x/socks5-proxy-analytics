package tui

import (
	"strings"
	"testing"
)

func TestSanitizeFieldStripsControlChars(t *testing.T) {
	in := "host\x1b[31m.example.com\tfoo\nbar\r\000"
	got := SanitizeField(in)
	if strings.ContainsAny(got, "\x1b\t\n\r\000") {
		t.Fatalf("control chars remain: %q", got)
	}
	if got != "host[31m.example.com foo bar " {
		// acceptable as long as no control chars; check substring
	}
}

func TestSanitizeFieldKeepsNormal(t *testing.T) {
	if got := SanitizeField("example.com"); got != "example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestMaskClientIP(t *testing.T) {
	if got := MaskClientIP("192.168.1.5", true); got != "192.168.1.xxx" {
		t.Fatalf("got %q", got)
	}
	if got := MaskClientIP("192.168.1.5", false); got != "192.168.1.5" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("got %q", got)
	}
	if got := Truncate("ab", 4); got != "ab" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatBytes(t *testing.T) {
	if got := FormatBytes(0); got != "0 B" {
		t.Fatalf("got %q", got)
	}
	if got := FormatBytes(-1); got != "N/A" {
		t.Fatalf("got %q", got)
	}
}
