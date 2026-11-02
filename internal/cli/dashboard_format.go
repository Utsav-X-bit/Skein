package cli

import (
	"strings"
	"unicode/utf8"
)

// uiUnicode selects the truncation marker. ASCII terminals get "...",
// everything else gets a single-cell ellipsis.
var uiUnicode = true

func ellipsisMark() string {
	if uiUnicode {
		return "…"
	}
	return "..."
}

// visibleWidth counts the terminal cells of s, ignoring SGR sequences.
func visibleWidth(s string) int {
	return utf8.RuneCountInString(stripANSI(s))
}

// clip truncates s to n visible cells. SGR sequences are preserved, and a
// truncation marker is appended when content is dropped.
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if visibleWidth(s) <= n {
		return s
	}
	mark := ellipsisMark()
	markWidth := utf8.RuneCountInString(mark)
	if n <= markWidth {
		// No room for both content and a marker; keep raw cells.
		return string([]rune(stripANSI(s))[:n])
	}
	keep := n - markWidth

	var b strings.Builder
	seen, escapes := 0, 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			b.WriteString(s[i:j])
			escapes++
			i = j
			continue
		}
		if seen >= keep {
			break
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		seen++
		i += size
	}
	b.WriteString(mark)
	if escapes > 0 {
		b.WriteString(ansiReset)
	}
	return b.String()
}

// fit pads or clips s to exactly n visible cells.
func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	out := clip(s, n)
	if width := visibleWidth(out); width < n {
		out += strings.Repeat(" ", n-width)
	}
	return out
}

// stripANSI removes CSI escape sequences (colors and cursor movement).
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			r, size := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += size
			continue
		}
		j := i + 1
		if j < len(s) && s[j] == '[' {
			j++
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
		}
		i = j
	}
	return b.String()
}

// compactFields renders a peer as two short lines for narrow cards: identity
// and link state first, measurements second.
func compactFields(name, state, transport, endpoint, rateLine string, width int) []string {
	line1 := " " + name
	if state != "" {
		line1 += " · " + state
		if transport != "" {
			line1 += " " + transport
		}
	}
	if endpoint != "" {
		if visibleWidth(line1)+1+visibleWidth(endpoint) <= width {
			line1 += " " + endpoint
		} else if minimal := " " + name + " " + endpoint; visibleWidth(minimal) <= width {
			// The address identifies the peer; drop state and transport
			// before dropping the address.
			line1 = minimal
		}
	}
	return []string{fit(line1, width), fit(" "+rateLine, width)}
}

// wrapVisible word-wraps s to width visible cells. SGR sequences travel with
// their word, so wrapped lines keep their color.
func wrapVisible(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	var cur strings.Builder
	curWidth := 0
	flush := func() {
		if curWidth > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
			curWidth = 0
		}
	}
	for _, word := range strings.Split(s, " ") {
		if word == "" {
			continue
		}
		w := visibleWidth(word)
		switch {
		case curWidth == 0:
			cur.WriteString(word)
			curWidth = w
		case curWidth+1+w <= width:
			cur.WriteByte(' ')
			cur.WriteString(word)
			curWidth += 1 + w
		default:
			flush()
			cur.WriteString(word)
			curWidth = w
		}
	}
	flush()
	return lines
}
