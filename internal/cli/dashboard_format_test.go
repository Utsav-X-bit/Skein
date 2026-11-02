package cli

import (
	"strings"
	"testing"
)

func withUnicode(t *testing.T, on bool) {
	t.Helper()
	old := uiUnicode
	uiUnicode = on
	t.Cleanup(func() { uiUnicode = old })
}

func TestClipPreservesColorAndEllipsis(t *testing.T) {
	withUnicode(t, true)
	in := "\x1b[32mconnected-and-healthy\x1b[0m"
	out := clip(in, 9)
	if got := visibleWidth(out); got != 9 {
		t.Fatalf("visible width = %d, want 9 (%q)", got, out)
	}
	if !strings.HasSuffix(stripANSI(out), "…") {
		t.Fatalf("missing ellipsis: %q", out)
	}
	if !strings.Contains(out, "\x1b[32m") {
		t.Fatalf("color was dropped: %q", out)
	}
	if !strings.Contains(out, ansiReset) {
		t.Fatalf("reset was dropped: %q", out)
	}
	if strings.Contains(stripANSI(out), "healthy") {
		t.Fatalf("clipped text kept too much: %q", stripANSI(out))
	}
}

func TestClipASCIIFallback(t *testing.T) {
	withUnicode(t, false)
	out := clip("abcdefgh", 6)
	if got := stripANSI(out); got != "abc..." {
		t.Fatalf("clip = %q", got)
	}
	if visibleWidth(out) != 6 {
		t.Fatalf("width = %d", visibleWidth(out))
	}
}

func TestFitPadsAndClipsToExactWidth(t *testing.T) {
	withUnicode(t, true)
	if got := fit("abc", 5); got != "abc  " {
		t.Fatalf("pad = %q", got)
	}
	if got := visibleWidth(fit("\x1b[31mabcdef\x1b[0m", 4)); got != 4 {
		t.Fatalf("clip width = %d", got)
	}
	if got := fit("", 0); got != "" {
		t.Fatalf("zero width = %q", got)
	}
}

func TestCompactFieldsYieldEndpointOnNarrowCards(t *testing.T) {
	wide := compactFields("yoga", "up", "udp", "10.144.144.2:11010", "↓ 1.2 kB/s  ↑ 300 B/s", 60)
	if !strings.Contains(wide[0], "10.144.144.2:11010") {
		t.Fatalf("wide line lost the endpoint: %q", wide[0])
	}
	narrow := compactFields("yoga", "up", "udp", "10.144.144.2:11010", "↓ 1.2 kB/s  ↑ 300 B/s", 20)
	if strings.Contains(narrow[0], "10.144.144.2") {
		t.Fatalf("narrow line kept the endpoint: %q", narrow[0])
	}
	if !strings.Contains(narrow[1], "1.2 kB/s") {
		t.Fatalf("narrow line lost the rate: %q", narrow[1])
	}
}
