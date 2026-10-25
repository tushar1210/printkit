package printkit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
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

	mu     sync.Mutex
	extra  map[string]*extraBrowser
	once   sync.Once
	closed bool
}

type extraBrowser struct {
	ctx    context.Context
	cancel context.CancelFunc
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
	root, cancel, err := launchBrowser(cfg, cfg.proxy)
	if err != nil {
		_ = assets.close()
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
	)
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
	go func() { errCh <- chromedp.Do(ctx) }()
	select {
	case err := <-errCh:
		if err != nil {
			cancel()
			allocCancel()
			return nil, nil, fmt.Errorf("printkit: start chromium: %w", err)
		}
	case <-time.After(30 * time.Second):
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
		c.extra = nil
		c.mu.Unlock()
		for _, b := range extra {
			b.cancel()
		}
		c.cancel()
		err = c.assets.close()
	})
	return err
}

func (c *Converter) ensureOpen() error {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	cfg := c.cfg
	cfg.userDataDir = ""
	ctx, cancel, err := launchBrowser(cfg, proxy)
	if err != nil {
		return nil, err
	}
	c.extra[proxy] = &extraBrowser{ctx: ctx, cancel: cancel}
	return ctx, nil
}

// withTab runs fn on a fresh tab. The tab is closed when fn returns.
// Concurrency is capped by the pool size.
func (c *Converter) withTab(ctx context.Context, proxy string, fn func(context.Context) error) error {
	if err := c.ensureOpen(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	parent, err := c.browser(proxy)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	}
	tab, cancelTab := chromedp.NewContext(parent)
	defer cancelTab()
	runCtx, stop := context.WithCancel(tab)
	defer stop()
	stopWatch := context.AfterFunc(ctx, func() {
		stop()
		cancelTab()
	})
	defer stopWatch()
	if c.cfg.defaultTimeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			var cancelTimeout context.CancelFunc
			runCtx, cancelTimeout = context.WithTimeout(runCtx, c.cfg.defaultTimeout)
			defer cancelTimeout()
		}
	}
	return fn(runCtx)
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
func (c *Converter) WritePDF(ctx context.Context, w io.Writer, req Request) error {
	if w == nil {
		return optionf("writer", "writer is nil")
	}
	data, err := c.renderPDF(ctx, req)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	if err != nil {
		return fmt.Errorf("printkit: write pdf: %w", err)
	}
	return nil
}

// SavePDF renders req and writes it to path, replacing any existing file.
func (c *Converter) SavePDF(ctx context.Context, path string, req Request) error {
	data, err := c.renderPDF(ctx, req)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
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
