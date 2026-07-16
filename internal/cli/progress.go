package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	buildrunner "github.com/12vault/ravel/internal/build"
)

type traversalProgress struct {
	w          io.Writer
	enabled    bool
	last       time.Time
	stage      string
	path       string
	count      int
	total      int
	unit       string
	second     int
	secondUnit string
	closed     bool
}

func newTraversalProgress(w io.Writer) *traversalProgress {
	p := &traversalProgress{w: w}
	if os.Getenv("TERM") == "dumb" {
		return p
	}
	file, ok := w.(*os.File)
	if !ok {
		return p
	}
	info, err := file.Stat()
	p.enabled = err == nil && info.Mode()&os.ModeCharDevice != 0
	return p
}

func (p *traversalProgress) Scan(path string, files int) {
	p.render("Scanning", path, files, 0, "files", 0, "")
}

func (p *traversalProgress) Build(event buildrunner.Progress) {
	p.render(event.Stage, event.Path, event.Completed, event.Total, event.Unit, event.Secondary, event.SecondaryUnit)
}

func (p *traversalProgress) render(stage, path string, completed, total int, unit string, secondary int, secondaryUnit string) {
	if !p.enabled {
		return
	}
	if p.closed {
		return
	}
	now := time.Now()
	sameStage := stage == p.stage
	p.stage = stage
	p.path = path
	p.count = completed
	p.total = total
	p.unit = unit
	if p.unit == "" {
		p.unit = "files"
	}
	p.second = secondary
	p.secondUnit = secondaryUnit
	finished := total > 0 && completed == total
	if sameStage && !finished && !p.last.IsZero() && now.Sub(p.last) < 45*time.Millisecond {
		return
	}
	p.draw(now)
}

func (p *traversalProgress) draw(now time.Time) {
	p.last = now
	count := fmt.Sprintf("%s %s", formatProgressNumber(p.count), p.unit)
	if p.total > 0 {
		count = fmt.Sprintf("%s/%s %s", formatProgressNumber(p.count), formatProgressNumber(p.total), p.unit)
	}
	if p.secondUnit != "" {
		count += fmt.Sprintf(" · %s %s", formatProgressNumber(p.second), p.secondUnit)
	}
	fmt.Fprintf(p.w, "\r\x1b[2K  %-22s %-28s %s", p.stage, count, shortenProgressPath(p.path, 72))
}

func formatProgressNumber(value int) string {
	text := fmt.Sprintf("%d", value)
	start := 0
	if strings.HasPrefix(text, "-") {
		start = 1
	}
	for i := len(text) - 3; i > start; i -= 3 {
		text = text[:i] + "," + text[i:]
	}
	return text
}

func (p *traversalProgress) Close() {
	if p.closed {
		return
	}
	p.closed = true
	if p.enabled {
		fmt.Fprint(p.w, "\r\x1b[2K")
	}
}

func shortenProgressPath(path string, limit int) string {
	if utf8.RuneCountInString(path) <= limit {
		return path
	}
	runes := []rune(path)
	return "…" + strings.TrimLeft(string(runes[len(runes)-limit+1:]), "/")
}
