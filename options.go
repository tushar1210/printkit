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
	trace          func(string, time.Duration)
	maxJobsPerTab  int
	rendererLimit  int
	rendererSet    bool
	jsHeapMB       int
	memoryLimit    uint64
	profileOwned   bool
}

func defaultConfig() config {
	return config{
		poolSize:       4,
		defaultTimeout: 60 * time.Second,
		logf:           func(string, ...any) {},
		// Reuse a tab this many times, then close it. One job per tab matches
		// the old behavior of opening a target and throwing it away.
		maxJobsPerTab: 50,
		// Cap the V8 heap. PDF rendering does not need a large JavaScript heap,
		// and an uncapped renderer is a large part of Chrome's RSS.
		jsHeapMB: 256,
		// Restart Chromium between jobs when its process tree exceeds this.
		// Zero, via WithMemoryLimit(0), disables the restart.
		memoryLimit: 2 << 30,
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

// WithRendererProcessLimit caps Chromium's renderer processes
// (--renderer-process-limit). The default is the pool size. Zero leaves the
// browser's own limit in place.
func WithRendererProcessLimit(n int) Option {
	return func(c *config) {
		if n < 0 {
			n = 0
		}
		c.rendererLimit = n
		c.rendererSet = true
	}
}

// WithMaxJobsPerTab closes a tab after it has rendered n documents and opens
// another one. The default is 50. Values below 1 close the tab after every job.
func WithMaxJobsPerTab(n int) Option {
	return func(c *config) {
		if n < 1 {
			n = 1
		}
		c.maxJobsPerTab = n
	}
}

// WithJSHeapLimit sets V8's --max-old-space-size in megabytes. The default is
// 256. Zero removes the cap.
func WithJSHeapLimit(megabytes int) Option {
	return func(c *config) {
		if megabytes < 0 {
			megabytes = 0
		}
		c.jsHeapMB = megabytes
	}
}

// WithMemoryLimit restarts Chromium after a job when the browser process tree
// exceeds bytes of RSS. The default is 2 GiB. Zero disables the restart.
// The in-flight job is not killed; the next job starts a fresh browser.
func WithMemoryLimit(bytes int64) Option {
	return func(c *config) {
		if bytes < 0 {
			bytes = 0
		}
		c.memoryLimit = uint64(bytes)
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
