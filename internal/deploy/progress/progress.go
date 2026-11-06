// Package progress writes a deploy's operator-facing progress lines to one writer
// and keeps the first write error, so a caller returns it instead of dropping it.
package progress

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Printer writes progress lines. It is safe for concurrent use.
type Printer struct {
	mu  sync.Mutex
	w   io.Writer
	err error
}

// New returns a Printer on w. A nil w writes to os.Stdout, so a caller that sets
// no writer keeps the CLI's output.
func New(w io.Writer) *Printer {
	if w == nil {
		w = os.Stdout
	}
	return &Printer{w: w}
}

// Printf writes one formatted line. After a failed write it writes nothing more.
func (p *Printer) Printf(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return
	}
	if _, err := fmt.Fprintf(p.w, format, args...); err != nil {
		p.err = fmt.Errorf("progress: write: %w", err)
	}
}

// Println writes args and a newline. After a failed write it writes nothing more.
func (p *Printer) Println(args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return
	}
	if _, err := fmt.Fprintln(p.w, args...); err != nil {
		p.err = fmt.Errorf("progress: write: %w", err)
	}
}

// Err returns the first write error, or nil.
func (p *Printer) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}
