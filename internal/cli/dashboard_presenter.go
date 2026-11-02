package cli

import (
	"fmt"
	"strings"
)

// framePresenter diffs complete rendered lines against the previous terminal
// frame. A full clear is emitted only on the first frame or a size change.
type framePresenter struct {
	last    []string
	width   int
	height  int
	started bool
}

// render returns the bytes needed to move the terminal to lines. Identical
// frames produce no output.
func (p *framePresenter) render(lines []string, width, height int) string {
	if !p.started || width != p.width || height != p.height {
		p.started = true
		p.width, p.height = width, height
		p.last = append(p.last[:0], lines...)

		var b strings.Builder
		b.WriteString("\x1b[2J\x1b[H")
		for i, line := range lines {
			if i > 0 {
				b.WriteString("\r\n")
			}
			b.WriteString(line)
			b.WriteString("\x1b[K")
		}
		return b.String()
	}

	var b strings.Builder
	for i := range lines {
		if i < len(p.last) && p.last[i] == lines[i] {
			continue
		}
		fmt.Fprintf(&b, "\x1b[%d;1H", i+1)
		b.WriteString(lines[i])
		b.WriteString("\x1b[K")
	}
	for i := len(lines); i < len(p.last); i++ {
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[K", i+1)
	}
	p.last = append(p.last[:0], lines...)
	return b.String()
}

// reset forgets the previous frame, forcing the next render to repaint.
func (p *framePresenter) reset() {
	p.started = false
	p.last = p.last[:0]
}
