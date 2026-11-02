package cli

import (
	"strings"
	"testing"
)

func TestPresenterFirstFrameClearsAndWritesAll(t *testing.T) {
	var p framePresenter
	out := p.render([]string{"one", "two"}, 10, 2)
	if !strings.Contains(out, "\x1b[2J") {
		t.Fatalf("first frame did not clear: %q", out)
	}
	if !strings.Contains(out, "one") || !strings.Contains(out, "two") {
		t.Fatalf("first frame lost lines: %q", out)
	}
}

func TestPresenterIdenticalFrameIsSilent(t *testing.T) {
	var p framePresenter
	p.render([]string{"one", "two"}, 10, 2)
	if out := p.render([]string{"one", "two"}, 10, 2); out != "" {
		t.Fatalf("identical frame emitted %q", out)
	}
}

func TestPresenterOnlyRewritesChangedRows(t *testing.T) {
	var p framePresenter
	p.render([]string{"one", "two", "three"}, 10, 3)
	out := p.render([]string{"one", "TWO", "three"}, 10, 3)
	if !strings.Contains(out, "\x1b[2;1H") {
		t.Fatalf("changed row was not rewritten: %q", out)
	}
	if strings.Contains(out, "\x1b[1;1H") || strings.Contains(out, "\x1b[3;1H") {
		t.Fatalf("unchanged rows were rewritten: %q", out)
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Fatalf("normal tick cleared the screen: %q", out)
	}
}

func TestPresenterResizeRepaintsFully(t *testing.T) {
	var p framePresenter
	p.render([]string{"one", "two"}, 10, 2)
	out := p.render([]string{"one", "two"}, 20, 2)
	if !strings.Contains(out, "\x1b[2J") || !strings.Contains(out, "one") {
		t.Fatalf("resize did not repaint: %q", out)
	}
}

func TestPresenterClearsRemovedRows(t *testing.T) {
	var p framePresenter
	p.render([]string{"one", "two", "three"}, 10, 3)
	out := p.render([]string{"one", "two"}, 10, 3)
	if !strings.Contains(out, "\x1b[3;1H\x1b[K") {
		t.Fatalf("removed row was not cleared: %q", out)
	}
}
