package printkit

import (
	"context"
	"fmt"
	"os"

	"github.com/chromedp/chromedp"
)

type tabCtl struct{ discard bool }

type tabCtlKey struct{}

func discardTab(ctx context.Context) {
	ctl, _ := ctx.Value(tabCtlKey{}).(*tabCtl)
	if ctl != nil {
		ctl.discard = true
	}
}

// Metrics counts tab and browser lifetime events since New.
type Metrics struct {
	TabsOpened      int
	TabsRetired     int
	BrowserRestarts int
}

// Metrics returns a snapshot of tab recycling and browser restarts.
func (c *Converter) Metrics() Metrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Metrics{
		TabsOpened:      c.tabsOpened,
		TabsRetired:     c.tabsRetired,
		BrowserRestarts: c.restarts,
	}
}

type tabHandle struct {
	ctx    context.Context
	cancel context.CancelFunc
	jobs   int
	proxy  string
}

// withTab runs fn on a tab. Tabs are reused until MaxJobsPerTab, then closed.
// Concurrency is capped by the pool size.
func (c *Converter) withTab(ctx context.Context, proxy string, fn func(context.Context) error) error {
	if err := c.ensureOpen(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	}
	h, err := c.acquireTab(proxy)
	if err != nil {
		return err
	}
	// The first action binds the tab's event loop to its context. A child
	// context that ends with the job would stop that loop and the next job
	// on the same tab would wait forever. Prime the tab with its own context,
	// then run the job on a child so a timeout still cancels the calls.
	if err := chromedp.Do(h.ctx); err != nil {
		c.releaseTab(h, true)
		return err
	}
	runCtx, stop := context.WithCancel(h.ctx)
	defer stop()
	stopWatch := context.AfterFunc(ctx, stop)
	defer stopWatch()
	if c.cfg.defaultTimeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			var cancelTimeout context.CancelFunc
			runCtx, cancelTimeout = context.WithTimeout(runCtx, c.cfg.defaultTimeout)
			defer cancelTimeout()
		}
	}
	ctl := &tabCtl{}
	runCtx = context.WithValue(runCtx, tabCtlKey{}, ctl)
	err = fn(runCtx)
	c.releaseTab(h, err != nil || ctx.Err() != nil || ctl.discard)
	return err
}

func (c *Converter) acquireTab(proxy string) (*tabHandle, error) {
	c.mu.Lock()
	if c.restartPending && c.active == 0 {
		c.restartLocked()
	}
	if c.startErr != nil {
		err := c.startErr
		c.mu.Unlock()
		return nil, err
	}
	for i, h := range c.idle {
		if h.proxy == proxy {
			c.idle = append(c.idle[:i], c.idle[i+1:]...)
			c.active++
			c.mu.Unlock()
			return h, nil
		}
	}
	c.active++
	c.mu.Unlock()

	parent, err := c.browser(proxy)
	if err != nil {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
		return nil, err
	}
	tab, cancel := chromedp.NewContext(parent)
	c.mu.Lock()
	c.tabsOpened++
	c.mu.Unlock()
	return &tabHandle{ctx: tab, cancel: cancel, proxy: proxy}, nil
}

func (c *Converter) releaseTab(h *tabHandle, discard bool) {
	h.jobs++
	over := c.overMemory()
	retire := discard || h.jobs >= c.cfg.maxJobsPerTab || over
	c.mu.Lock()
	c.active--
	if !retire {
		c.idle = append(c.idle, h)
		c.mu.Unlock()
		return
	}
	c.tabsRetired++
	pending := over || c.restartPending
	idle := c.active == 0
	c.mu.Unlock()
	h.cancel()
	if pending && idle {
		c.mu.Lock()
		if c.active == 0 && !c.closed {
			c.restartLocked()
		} else {
			c.restartPending = true
		}
		c.mu.Unlock()
	} else if pending {
		c.mu.Lock()
		c.restartPending = true
		c.mu.Unlock()
	}
}

func (c *Converter) overMemory() bool {
	c.mu.Lock()
	limit := c.cfg.memoryLimit
	dir := c.cfg.userDataDir
	c.mu.Unlock()
	if limit == 0 {
		return false
	}
	return rssForProfile(dir) > limit
}

// restartLocked replaces the browser. The caller holds c.mu.
// The old process is stopped before the new one starts so the two do not
// share the RSS budget. active is zero whenever this runs.
func (c *Converter) restartLocked() {
	c.restartPending = false
	for _, h := range c.idle {
		h.cancel()
	}
	c.idle = nil
	oldExtra := c.extra
	c.extra = map[string]*extraBrowser{}
	oldCancel := c.cancel
	oldDir := ""
	if c.cfg.profileOwned {
		oldDir = c.cfg.userDataDir
		dir, err := os.MkdirTemp("", "printkit-*")
		if err != nil {
			c.startErr = fmt.Errorf("printkit: profile: %w", err)
			c.closed = true
			if oldCancel != nil {
				oldCancel()
				c.cancel = nil
			}
			for _, b := range oldExtra {
				b.cancel()
				if b.dir != "" {
					_ = os.RemoveAll(b.dir)
				}
			}
			return
		}
		c.cfg.userDataDir = dir
	}
	if oldCancel != nil {
		oldCancel()
		c.cancel = nil
		c.root = context.Background()
	}
	for _, b := range oldExtra {
		b.cancel()
		if b.dir != "" {
			_ = os.RemoveAll(b.dir)
		}
	}
	ctx, cancel, err := launchBrowser(c.cfg, c.cfg.proxy)
	if oldDir != "" {
		_ = os.RemoveAll(oldDir)
	}
	if err != nil {
		c.startErr = err
		c.closed = true
		return
	}
	c.root = ctx
	c.cancel = cancel
	c.startErr = nil
	c.restarts++
}
