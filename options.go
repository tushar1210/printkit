package printkit

import (
	"fmt"
	"io"
	"time"
)

// Option configures a Converter.
type Option func(*config)

type config struct {
	chromePath     string
	chromeSet      bool
	poolSize       int
	noSandbox      bool
	noSandboxSet   bool
	chromeArgs     []string
	defaultTimeout time.Duration
	timeoutSet     bool
	proxy          string
	userDataDir    string
	env            []string
	logf           func(string, ...any)
}

func defaultConfig() config {
	return config{
		poolSize:       4,
		defaultTimeout: 60 * time.Second,
		logf:           func(string, ...any) {},
	}
}

// WithChromePath launches this binary instead of the one FindChrome discovers.
func WithChromePath(path string) Option {
	return func(c *config) {
		c.chromePath = path
		c.chromeSet = true
	}
}

// WithPoolSize limits how many tabs convert at once. Values below 1 become 1.
func WithPoolSize(n int) Option {
	return func(c *config) {
		if n < 1 {
			n = 1
		}
		c.poolSize = n
	}
}

// WithNoSandbox sets Chromium's --no-sandbox flag. Containers that run as
// root, and some restricted sandboxes, cannot start the Chromium sandbox.
// Leave it off when the process can use the sandbox. Running as root or
// setting PRINTKIT_NO_SANDBOX=1 selects it automatically unless this option
// is passed.
func WithNoSandbox(noSandbox bool) Option {
	return func(c *config) {
		c.noSandbox = noSandbox
		c.noSandboxSet = true
	}
}

// WithChromeArgs appends extra command-line flags. Each argument is one flag
// in the form Chromium expects, for example "--font-render-hinting=none".
func WithChromeArgs(args ...string) Option {
	return func(c *config) { c.chromeArgs = append(c.chromeArgs, args...) }
}

// WithDefaultTimeout bounds a conversion whose context and request have no
// deadline. Zero disables the extra deadline. The default is 60 seconds.
func WithDefaultTimeout(d time.Duration) Option {
	return func(c *config) {
		c.defaultTimeout = d
		c.timeoutSet = true
	}
}

// WithProxy sets the default Chromium proxy server, for example
// "http://127.0.0.1:8888". A request can override it with Network.Proxy.
func WithProxy(proxyURL string) Option {
	return func(c *config) { c.proxy = proxyURL }
}

// WithUserDataDir sets Chromium's profile directory. The default is a fresh
// temporary profile per process.
func WithUserDataDir(dir string) Option {
	return func(c *config) { c.userDataDir = dir }
}

// WithEnv adds environment variables to the Chromium process. Each entry has
// the form "KEY=value".
func WithEnv(env []string) Option {
	return func(c *config) { c.env = append(c.env, env...) }
}

// WithLogf receives Chromium and converter diagnostics. The default discards
// them. The function must be safe to call from more than one goroutine.
func WithLogf(f func(string, ...any)) Option {
	return func(c *config) {
		if f == nil {
			f = func(string, ...any) {}
		}
		c.logf = f
	}
}

// WithStderr is accepted as a convenience for callers that already have a
// writer. Lines are written with a trailing newline.
func WithStderr(w io.Writer) Option {
	return WithLogf(func(format string, args ...any) {
		if w == nil {
			return
		}
		_, _ = io.WriteString(w, fmt.Sprintf(format, args...)+"\n")
	})
}
