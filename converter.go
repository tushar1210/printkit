package printkit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// Converter renders documents with one shared Chromium process.
// A Converter is safe for concurrent use. Close releases the process and
// aborts conversions that are still running.
type Converter struct {
	root   context.Context
	cancel context.CancelFunc
	cfg    config
	sem    chan struct{}
	assets *assetServer
	logf   func(string, ...any)

	mu             sync.Mutex
	extra          map[string]*extraBrowser
	idle           []*tabHandle
	active         int
	tabsOpened     int
	tabsRetired    int
	restarts       int
	restartPending bool
	startErr       error
	once           sync.Once
	closed         bool
}

type extraBrowser struct {
	ctx    context.Context
	cancel context.CancelFunc
	dir    string
}

// New starts Chromium and returns a converter. The first call discovers the
// browser, unless WithChromePath was set, and launches it. Close must be
// called, usually with defer.
func New(opts ...Option) (*Converter, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if !cfg.noSandboxSet && (os.Geteuid() == 0 || envTruthy("PRINTKIT_NO_SANDBOX")) {
		cfg.noSandbox = true
	}
	if cfg.trace == nil && envTruthy("PRINTKIT_TRACE") {
		cfg.trace = stderrTrace
	}
	path := cfg.chromePath
	if !cfg.chromeSet || path == "" {
		found, err := FindChrome()
		if err != nil {
			return nil, err
		}
		path = found
		cfg.chromePath = path
	} else if _, ok := existingBinary(path); !ok {
		return nil, fmt.Errorf("%w: %s", ErrChromeNotFound, path)
	}

	assets, err := newAssetServer()
	if err != nil {
		return nil, err
	}
	if cfg.userDataDir == "" {
		dir, err := os.MkdirTemp("", "printkit-*")
		if err != nil {
			_ = assets.close()
			return nil, fmt.Errorf("printkit: profile: %w", err)
		}
		cfg.userDataDir = dir
		cfg.profileOwned = true
	}
	root, cancel, err := launchBrowser(cfg, cfg.proxy)
	if err != nil {
		_ = assets.close()
		if cfg.profileOwned {
			_ = os.RemoveAll(cfg.userDataDir)
		}
		return nil, err
	}
	c := &Converter{
		root:   root,
		cancel: cancel,
		cfg:    cfg,
		sem:    make(chan struct{}, cfg.poolSize),
		assets: assets,
		logf:   cfg.logf,
		extra:  map[string]*extraBrowser{},
	}
	return c, nil
}

func envTruthy(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func launchBrowser(cfg config, proxy string) (context.Context, context.CancelFunc, error) {
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.ExecPath(cfg.chromePath),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1280, 800),
		// Several of these are also in chromedp's defaults. Setting them here
		// keeps the lean set intact when a later Flag replaces one by name.
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("disable-domain-reliability", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-component-extensions-with-background-pages", true),
		chromedp.Flag("disable-breakpad", true),
		chromedp.Flag("disable-client-side-phishing-detection", true),
		chromedp.Flag("disable-hang-monitor", true),
		chromedp.Flag("disable-prompt-on-repost", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
		chromedp.Flag("metrics-recording-only", true),
		chromedp.Flag("safebrowsing-disable-auto-update", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-search-engine-choice-screen", true),
		// Flag replaces by name, so this has to include chromedp's defaults.
		chromedp.Flag("disable-features", "Translate,BlinkGenPropertyTrees,BackForwardCache,AcceptCHFrame,MediaRouter,OptimizationHints,IsolateSandboxedIframes,DialMediaRouteProvider"),
	)
	limit := cfg.poolSize
	if cfg.rendererSet {
		limit = cfg.rendererLimit
	}
	if limit > 0 {
		opts = append(opts, chromedp.Flag("renderer-process-limit", strconv.Itoa(limit)))
	}
	if cfg.jsHeapMB > 0 {
		opts = append(opts, chromedp.Flag("js-flags", fmt.Sprintf("--max-old-space-size=%d", cfg.jsHeapMB)))
	}
	if len(cfg.env) > 0 {
		opts = append(opts, chromedp.Env(cfg.env...))
	}
	if cfg.noSandbox {
		opts = append(opts, chromedp.NoSandbox)
	}
	if proxy != "" {
		opts = append(opts, chromedp.ProxyServer(proxy))
	}
	if cfg.userDataDir != "" {
		opts = append(opts, chromedp.UserDataDir(cfg.userDataDir))
	}
	for _, arg := range cfg.chromeArgs {
		name, value, ok := splitChromeArg(arg)
		if !ok {
			return nil, nil, optionf("ChromeArgs", "flag %q must look like --name or --name=value", arg)
		}
		opts = append(opts, chromedp.Flag(name, value))
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx,
		chromedp.WithLogf(cfg.logf),
		chromedp.WithErrorf(cfg.logf),
		chromedp.WithDebugf(cfg.logf),
	)
	// The first Run binds the browser's lifetime to its context, so it has to
	// be the long-lived context. A child with a timeout would stop Chromium
	// as soon as the deadline fired.
	errCh := make(chan error, 1)
	stopLaunch := traceDone(cfg.trace, "launch")
	go func() { errCh <- chromedp.Do(ctx) }()
	select {
	case err := <-errCh:
		stopLaunch()
		if err != nil {
			cancel()
			allocCancel()
			return nil, nil, fmt.Errorf("printkit: start chromium: %w", err)
		}
	case <-time.After(30 * time.Second):
		stopLaunch()
		cancel()
		allocCancel()
		return nil, nil, fmt.Errorf("printkit: start chromium: timed out")
	}
	return ctx, func() {
		cancel()
		allocCancel()
	}, nil
}

func splitChromeArg(arg string) (string, any, bool) {
	if !strings.HasPrefix(arg, "--") || len(arg) < 3 {
		return "", nil, false
	}
	body := strings.TrimPrefix(arg, "--")
	name, value, ok := strings.Cut(body, "=")
	if !ok {
		return name, true, name != ""
	}
	return name, value, name != ""
}

// Close stops Chromium and the loopback file server. In-flight conversions
// are canceled. Close is safe to call more than once.
func (c *Converter) Close() error {
	var err error
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		extra := c.extra
		idle := c.idle
		c.extra = nil
		c.idle = nil
		cancel := c.cancel
		dir := ""
		if c.cfg.profileOwned {
			dir = c.cfg.userDataDir
		}
		c.mu.Unlock()
		for _, h := range idle {
			h.cancel()
		}
		for _, b := range extra {
			b.cancel()
			if b.dir != "" {
				_ = os.RemoveAll(b.dir)
			}
		}
		if cancel != nil {
			cancel()
		}
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
		err = c.assets.close()
	})
	return err
}

func (c *Converter) ensureOpen() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.startErr != nil {
		return c.startErr
	}
	if c.closed {
		return ErrClosed
	}
	return nil
}

func (c *Converter) browser(proxy string) (context.Context, error) {
	if err := c.ensureOpen(); err != nil {
		return nil, err
	}
	if proxy == "" || proxy == c.cfg.proxy {
		return c.root, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if b, ok := c.extra[proxy]; ok {
		return b.ctx, nil
	}
	dir, err := os.MkdirTemp("", "printkit-proxy-*")
	if err != nil {
		return nil, fmt.Errorf("printkit: profile: %w", err)
	}
	cfg := c.cfg
	cfg.userDataDir = dir
	cfg.profileOwned = true
	ctx, cancel, err := launchBrowser(cfg, proxy)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	c.extra[proxy] = &extraBrowser{ctx: ctx, cancel: cancel, dir: dir}
	return ctx, nil
}

// PDF renders req to a PDF document.
func (c *Converter) PDF(ctx context.Context, req Request) ([]byte, error) {
	var buf bytes.Buffer
	if err := c.WritePDF(ctx, &buf, req); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WritePDF renders req and writes the PDF to w.
// A request that needs no merge, outline, metadata, or page-range pass streams
// Page.printToPDF into w. Anything else is rendered fully, then written.
func (c *Converter) WritePDF(ctx context.Context, w io.Writer, req Request) error {
	if w == nil {
		return optionf("writer", "writer is nil")
	}
	if canDirectWrite(req) {
		return c.writePDFDirect(ctx, w, req)
	}
	data, err := c.renderPDF(ctx, req)
	if err != nil {
		return err
	}
	stopWrite := traceDone(c.cfg.trace, "write")
	_, err = w.Write(data)
	stopWrite()
	if err != nil {
		return fmt.Errorf("printkit: write pdf: %w", err)
	}
	return nil
}

// SavePDF renders req and writes it to path, replacing any existing file.
func (c *Converter) SavePDF(ctx context.Context, path string, req Request) error {
	if canDirectWrite(req) {
		tmp := path + ".printkit-tmp"
		f, err := os.Create(tmp)
		if err != nil {
			return fmt.Errorf("printkit: write %s: %w", path, err)
		}
		werr := c.writePDFDirect(ctx, f, req)
		cerr := f.Close()
		if werr != nil {
			_ = os.Remove(tmp)
			return werr
		}
		if cerr != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("printkit: write %s: %w", path, cerr)
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("printkit: write %s: %w", path, err)
		}
		return nil
	}
	data, err := c.renderPDF(ctx, req)
	if err != nil {
		return err
	}
	stopWrite := traceDone(c.cfg.trace, "write")
	err = writeFileAtomic(path, data)
	stopWrite()
	return err
}

// Image renders req to an encoded image.
func (c *Converter) Image(ctx context.Context, req ImageRequest) ([]byte, error) {
	var buf bytes.Buffer
	if err := c.WriteImage(ctx, &buf, req); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteImage renders req and writes the encoded image to w.
func (c *Converter) WriteImage(ctx context.Context, w io.Writer, req ImageRequest) error {
	if w == nil {
		return optionf("writer", "writer is nil")
	}
	data, err := c.renderImage(ctx, req)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	if err != nil {
		return fmt.Errorf("printkit: write image: %w", err)
	}
	return nil
}

// SaveImage renders req and writes it to path.
func (c *Converter) SaveImage(ctx context.Context, path string, req ImageRequest) error {
	data, err := c.renderImage(ctx, req)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// ConvertPDF is a one-shot PDF conversion. It starts Chromium and stops it
// before returning. Use New when you convert more than once.
func ConvertPDF(ctx context.Context, req Request, opts ...Option) ([]byte, error) {
	c, err := New(opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	return c.PDF(ctx, req)
}

// ConvertImage is a one-shot image conversion.
func ConvertImage(ctx context.Context, req ImageRequest, opts ...Option) ([]byte, error) {
	c, err := New(opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	return c.Image(ctx, req)
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".printkit-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("printkit: write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("printkit: write %s: %w", path, err)
	}
	return nil
}
