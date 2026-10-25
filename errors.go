package printkit

import "fmt"

var (
	// ErrChromeNotFound is returned when no Chromium or Chrome binary can be
	// located. Set CHROME_PATH or pass WithChromePath.
	ErrChromeNotFound = fmt.Errorf("printkit: chromium not found")

	// ErrClosed is returned when a method is called on a Converter that has
	// been closed.
	ErrClosed = fmt.Errorf("printkit: converter is closed")

	// ErrNoInput is returned when a request has no document to render.
	ErrNoInput = fmt.Errorf("printkit: no input document")

	// ErrFileAccessDenied is returned when a local file or file:// URL is
	// requested and local file access is disabled.
	ErrFileAccessDenied = fmt.Errorf("printkit: local file access is disabled")

	// ErrInvalidOption is returned for rejected options. Use errors.As with
	// *OptionError for the field name.
	ErrInvalidOption = fmt.Errorf("printkit: invalid option")

	// ErrLoadFailed is returned when a document fails to load and the request
	// policy is LoadErrorAbort. Use errors.As with *LoadError for the URL and
	// status.
	ErrLoadFailed = fmt.Errorf("printkit: page load failed")
)

// OptionError describes one rejected option.
type OptionError struct {
	Field  string
	Reason string
}

func (e *OptionError) Error() string {
	if e.Field == "" {
		return "printkit: invalid option: " + e.Reason
	}
	return "printkit: invalid option " + e.Field + ": " + e.Reason
}

func (e *OptionError) Unwrap() error { return ErrInvalidOption }

// LoadError is a failed navigation or an HTTP error response for the main
// document.
type LoadError struct {
	URL    string
	Status int
	Detail string
}

func (e *LoadError) Error() string {
	switch {
	case e.Status != 0 && e.Detail != "":
		return fmt.Sprintf("printkit: load %s: HTTP %d: %s", e.URL, e.Status, e.Detail)
	case e.Status != 0:
		return fmt.Sprintf("printkit: load %s: HTTP %d", e.URL, e.Status)
	case e.Detail != "":
		return fmt.Sprintf("printkit: load %s: %s", e.URL, e.Detail)
	default:
		return "printkit: load failed"
	}
}

func (e *LoadError) Unwrap() error { return ErrLoadFailed }

func optionf(field, format string, args ...any) error {
	return &OptionError{Field: field, Reason: fmt.Sprintf(format, args...)}
}
