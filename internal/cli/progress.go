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
	w       io.Writer
	enabled bool
	last    time.Time
	frame   int
	stage   string
	closed  bool
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
	p.render("Scanning", path, files, 0)
}

func (p *traversalProgress) Build(event buildrunner.Progress) {
	p.render(event.Stage, event.Path, event.Completed, event.Total)
}

func (p *traversalProgress) render(stage, path string, completed, total int) {
	if !p.enabled {
		return
	}
	now := time.Now()
	if stage == p.stage && !p.last.IsZero() && now.Sub(p.last) < 45*time.Millisecond {
		return
	}
	p.last = now
	p.stage = stage
	spinner := []string{"◐", "◓", "◑", "◒"}
	count := fmt.Sprintf("%d files", completed)
	if total > 0 {
		count = fmt.Sprintf("%d/%d files", completed, total)
	}
	fmt.Fprintf(p.w, "\r\x1b[2K  %s %-18s %-15s %s", spinner[p.frame%len(spinner)], stage, count, shortenProgressPath(path, 72))
	p.frame++
}

func (p *traversalProgress) Close() {
	if p.enabled && !p.closed {
		fmt.Fprint(p.w, "\r\x1b[2K")
	}
	p.closed = true
}

func shortenProgressPath(path string, limit int) string {
	if utf8.RuneCountInString(path) <= limit {
		return path
	}
	runes := []rune(path)
	return "…" + strings.TrimLeft(string(runes[len(runes)-limit+1:]), "/")
}
