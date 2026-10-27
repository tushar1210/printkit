package printkit

import (
	"fmt"
	"io"
	"os"
	"time"
)

// Span is one timed step of a conversion.
type Span struct {
	Name     string
	Duration time.Duration
}

// WithTrace records each timed step. The function may run on more than one
// goroutine. PRINTKIT_TRACE=1 logs the same steps to stderr when WithTrace
// is not set.
func WithTrace(fn func(name string, d time.Duration)) Option {
	return func(c *config) {
		if fn == nil {
			fn = func(string, time.Duration) {}
		}
		c.trace = fn
	}
}

func stderrTrace(name string, d time.Duration) {
	fmt.Fprintf(os.Stderr, "printkit: %s %s\n", name, d.Round(time.Millisecond))
}

func traceDone(fn func(string, time.Duration), name string) func() {
	if fn == nil {
		return func() {}
	}
	start := time.Now()
	return func() { fn(name, time.Since(start)) }
}

// FormatTrace writes spans as "name duration" lines.
func FormatTrace(w io.Writer, spans []Span) error {
	for _, s := range spans {
		if _, err := fmt.Fprintf(w, "%s %s\n", s.Name, s.Duration.Round(time.Microsecond)); err != nil {
			return err
		}
	}
	return nil
}
