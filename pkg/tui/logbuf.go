package tui

import (
	"strings"
	"sync"
)

// LogBuffer collects log output (logging.SetOutput) while the TUI owns the
// terminal; the Log tab shows its last lines. Safe for concurrent use.
type LogBuffer struct {
	mu      sync.Mutex
	lines   []string
	partial string
	max     int
}

// NewLogBuffer keeps the last max lines (500 when max <= 0).
func NewLogBuffer(max int) *LogBuffer {
	if max <= 0 {
		max = 500
	}
	return &LogBuffer{max: max}
}

// Write implements io.Writer.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text := b.partial + string(p)
	parts := strings.Split(text, "\n")
	b.partial = parts[len(parts)-1]
	b.lines = append(b.lines, parts[:len(parts)-1]...)
	if over := len(b.lines) - b.max; over > 0 {
		b.lines = append([]string(nil), b.lines[over:]...)
	}
	return len(p), nil
}

// Lines returns the complete lines collected so far.
func (b *LogBuffer) Lines() []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.lines...)
}
