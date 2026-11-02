package cli

import "time"

var unicodeMark = []string{
	"╭╲╲    ╱╱╮",
	" ╲╲╲╳╱╱╱ ",
	"╰╱╱    ╲╲╯",
}

var asciiMark = []string{
	"/\\\\    //\\",
	" \\\\--// ",
	"\\//    \\//",
}

// brandLines returns a terminal-safe, outer-to-centre weave reveal. It has a
// complete static end frame for no-motion and non-Unicode terminals.
func brandLines(elapsed time.Duration, unicode, motion bool) []string {
	mark := unicodeMark
	if !unicode {
		mark = asciiMark
	}
	if !motion || elapsed >= 600*time.Millisecond {
		return []string{mark[0] + "  Skein", mark[1] + "  MESH VPN", mark[2]}
	}
	stage := int(elapsed / (60 * time.Millisecond))
	if stage < 1 {
		return []string{"", "", ""}
	}
	out := []string{"", "", ""}
	if stage >= 1 {
		out[0] = mark[0]
	}
	if stage >= 3 {
		out[2] = mark[2]
	}
	if stage >= 5 {
		out[1] = mark[1]
	}
	if elapsed >= 420*time.Millisecond {
		out[0] += "  Skein"
		out[1] += "  MESH VPN"
	}
	return out
}

func (d *dashboard) renderBrand(v view, r dashboardRect) []string {
	if r.h < 1 {
		return nil
	}
	lines := make([]string, r.h)
	brand := brandLines(time.Since(d.started), d.unicode, v.motion && !v.ui.paused)
	meta := ""
	if v.st.ID != "" {
		meta = v.st.ID + " · " + dash(v.st.OverlayIPv4)
	}
	if len(brand) > 1 && meta != "" {
		brand[1] += "  " + meta
	}
	for i := 0; i < len(brand) && i < len(lines); i++ {
		code := ansiBrand
		if i == 1 && meta != "" {
			code = ansiWhite
		}
		lines[i] = d.paint(code, fit(" "+brand[i], r.w))
	}
	return padPanel(lines, r)
}
