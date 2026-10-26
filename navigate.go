package printkit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	cdptype "github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// errSkipInput tells a multi-input render to drop this document.
var errSkipInput = errors.New("printkit: skipping input")

type navSpec struct {
	in            Input
	network       Network
	wait          Wait
	inject        Inject
	media         Media
	grayscale     bool
	viewportW     int
	viewportH     int
	deviceScale   float64
	transparent   bool
	stripExternal bool
	stripInternal bool
	load          LoadErrorPolicy
	minFontPx     float64
}

type pageInfo struct {
	Location string
	Title    string
}

func (c *Converter) open(ctx context.Context, spec navSpec) (pageInfo, func(), error) {
	rawURL, display, cleanup, err := c.resolve(spec.in, spec.network)
	if err != nil {
		return pageInfo{}, noop, err
	}
	navURL := withUserInfo(rawURL, spec.network)
	if err := prepareTarget(ctx, spec, navURL); err != nil {
		cleanup()
		return pageInfo{}, noop, err
	}
	resp, err := chromedp.Run(ctx, chromedp.NavigateResponse(navURL))
	if err != nil {
		cleanup()
		return pageInfo{}, noop, c.finishLoad(display, spec.load, err)
	}
	if resp != nil && resp.Status >= 400 {
		loadErr := &LoadError{URL: display, Status: int(resp.Status), Detail: resp.StatusText}
		if handled := c.finishLoad(display, spec.load, loadErr); handled != nil {
			cleanup()
			return pageInfo{}, noop, handled
		}
	}
	if err := settle(ctx, spec.wait); err != nil {
		cleanup()
		return pageInfo{}, noop, err
	}
	if err := decorate(ctx, spec); err != nil {
		cleanup()
		return pageInfo{}, noop, err
	}
	title, err := chromedp.Run(ctx, chromedp.Title())
	if err != nil {
		cleanup()
		return pageInfo{}, noop, fmt.Errorf("printkit: document title: %w", err)
	}
	return pageInfo{Location: display, Title: title}, cleanup, nil
}

func noop() {}

func (c *Converter) finishLoad(display string, policy LoadErrorPolicy, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var load *LoadError
	var chromeLoad *chromedp.LoadError
	switch {
	case errors.As(err, &load):
	case errors.As(err, &chromeLoad):
		load = &LoadError{URL: display, Detail: chromeLoad.ErrorText}
		err = load
	default:
		load = &LoadError{URL: display, Detail: err.Error()}
		err = load
	}
	switch policy {
	case LoadErrorIgnore:
		return nil
	case LoadErrorSkip:
		return fmt.Errorf("%w: %s", errSkipInput, load.Error())
	default:
		return err
	}
}

func (c *Converter) resolve(in Input, net Network) (rawURL, display string, cleanup func(), err error) {
	cleanup = noop
	n := 0
	if in.URL != "" {
		n++
	}
	if in.File != "" {
		n++
	}
	if in.HTML != "" {
		n++
	}
	if in.Reader != nil {
		n++
	}
	if n == 0 {
		return "", "", cleanup, ErrNoInput
	}
	if n != 1 {
		return "", "", cleanup, optionf("Input", "set only one of URL, File, HTML, or Reader")
	}
	allow := boolOr(net.AllowFileAccess, true)
	switch {
	case in.URL != "":
		u, err := url.Parse(in.URL)
		if err != nil || u.Scheme == "" {
			return "", "", cleanup, optionf("URL", "invalid URL %q", in.URL)
		}
		if u.Scheme == "file" {
			if !allow {
				return "", "", cleanup, ErrFileAccessDenied
			}
			path := fileURLPath(u)
			if !pathAllowed(path, net.AllowPaths) {
				return "", "", cleanup, ErrFileAccessDenied
			}
			return c.serveFile(path)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", "", cleanup, optionf("URL", "unsupported scheme %q", u.Scheme)
		}
		return in.URL, in.URL, cleanup, nil
	case in.File != "":
		if !allow {
			return "", "", cleanup, ErrFileAccessDenied
		}
		if !pathAllowed(in.File, net.AllowPaths) {
			return "", "", cleanup, ErrFileAccessDenied
		}
		return c.serveFile(in.File)
	default:
		var body []byte
		if in.Reader != nil {
			body, err = readAllLimited(in.Reader)
			if err != nil {
				return "", "", cleanup, err
			}
		} else {
			body = []byte(in.HTML)
		}
		if strings.TrimSpace(string(body)) == "" {
			return "", "", cleanup, optionf("HTML", "document is empty")
		}
		htmlDoc := string(body)
		var cleanups []func()
		if in.BaseDir != "" {
			if !allow {
				return "", "", cleanup, ErrFileAccessDenied
			}
			if !pathAllowed(in.BaseDir, net.AllowPaths) {
				return "", "", cleanup, ErrFileAccessDenied
			}
			dirMount, err := c.assets.mountDir(in.BaseDir, "")
			if err != nil {
				return "", "", cleanup, err
			}
			cleanups = append(cleanups, dirMount.cleanup)
			htmlDoc = injectBase(htmlDoc, dirMount.url)
		}
		mounted := c.assets.mountHTML(htmlDoc)
		cleanups = append(cleanups, mounted.cleanup)
		display = "html"
		if in.BaseDir != "" {
			display = in.BaseDir
		}
		return mounted.url, display, func() {
			for _, fn := range cleanups {
				fn()
			}
		}, nil
	}
}

func (c *Converter) serveFile(path string) (string, string, func(), error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", noop, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", noop, fmt.Errorf("printkit: open %s: %w", path, err)
	}
	if info.IsDir() {
		return "", "", noop, optionf("File", "%s is a directory", path)
	}
	mounted, err := c.assets.mountDir(filepath.Dir(abs), filepath.Base(abs))
	if err != nil {
		return "", "", noop, err
	}
	return mounted.url, abs, mounted.cleanup, nil
}

func fileURLPath(u *url.URL) string {
	p := u.Path
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

func pathAllowed(path string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	for _, root := range roots {
		r, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		r = filepath.Clean(r)
		if abs == r || strings.HasPrefix(abs, r+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func injectBase(doc, base string) string {
	tag := `<base href="` + html.EscapeString(base) + `">`
	lower := strings.ToLower(doc)
	if i := strings.Index(lower, "<head>"); i >= 0 {
		return doc[:i+len("<head>")] + tag + doc[i+len("<head>"):]
	}
	if i := strings.Index(lower, "<head "); i >= 0 {
		if j := strings.Index(doc[i:], ">"); j >= 0 {
			at := i + j + 1
			return doc[:at] + tag + doc[at:]
		}
	}
	return "<head>" + tag + "</head>" + doc
}

func prepareTarget(ctx context.Context, spec navSpec, rawURL string) error {
	if _, err := chromedp.Call(ctx, network.Enable, network.EnableParams{}); err != nil {
		return fmt.Errorf("printkit: network: %w", err)
	}
	headers := map[string]any{}
	for k, v := range spec.network.Headers {
		headers[k] = v
	}
	if spec.network.Username != "" || spec.network.Password != "" {
		if _, ok := headers["Authorization"]; !ok {
			token := base64.StdEncoding.EncodeToString([]byte(spec.network.Username + ":" + spec.network.Password))
			headers["Authorization"] = "Basic " + token
		}
	}
	if len(headers) > 0 {
		if _, err := chromedp.Call(ctx, network.SetExtraHTTPHeaders, network.SetExtraHTTPHeadersParams{
			Headers: network.Headers(headers),
		}); err != nil {
			return fmt.Errorf("printkit: headers: %w", err)
		}
	}
	for _, cookie := range spec.network.Cookies {
		if err := setCookie(ctx, cookie, rawURL); err != nil {
			return err
		}
	}
	if spec.network.UserAgent != "" {
		if _, err := chromedp.Call(ctx, emulation.SetUserAgentOverride, emulation.SetUserAgentOverrideParams{
			UserAgent: spec.network.UserAgent,
		}); err != nil {
			return fmt.Errorf("printkit: user agent: %w", err)
		}
	}
	if !boolOr(spec.network.JavaScript, true) {
		if _, err := chromedp.Call(ctx, emulation.SetScriptExecutionDisabled, emulation.SetScriptExecutionDisabledParams{Value: true}); err != nil {
			return fmt.Errorf("printkit: disable javascript: %w", err)
		}
	}
	if !boolOr(spec.network.Images, true) {
		patterns := []*network.BlockPattern{}
		for _, ext := range []string{"png", "jpg", "jpeg", "gif", "webp", "svg", "avif", "bmp", "ico"} {
			patterns = append(patterns, &network.BlockPattern{URLPattern: "*://*/*." + ext, Block: true})
		}
		if _, err := chromedp.Call(ctx, network.SetBlockedURLs, network.SetBlockedURLsParams{URLPatterns: patterns}); err != nil {
			return fmt.Errorf("printkit: block images: %w", err)
		}
	}
	media := string(spec.media)
	if media == "" {
		media = string(MediaPrint)
	}
	if _, err := chromedp.Call(ctx, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{Media: media}); err != nil {
		return fmt.Errorf("printkit: media: %w", err)
	}
	if spec.viewportW > 0 || spec.viewportH > 0 || spec.deviceScale > 0 {
		w, h := spec.viewportW, spec.viewportH
		if w <= 0 {
			w = 1280
		}
		if h <= 0 {
			h = 800
		}
		scale := spec.deviceScale
		if scale <= 0 {
			scale = 1
		}
		if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
			Width: int64(w), Height: int64(h), DeviceScaleFactor: scale,
		}); err != nil {
			return fmt.Errorf("printkit: viewport: %w", err)
		}
	}
	if spec.transparent {
		if err := setTransparentBackground(ctx); err != nil {
			return err
		}
	}
	css, _, err := spec.inject.combined()
	if err != nil {
		return err
	}
	if css != "" {
		if err := addEarlyCSS(ctx, css); err != nil {
			return err
		}
	}
	return nil
}

func withUserInfo(rawURL string, net Network) string {
	if net.Username == "" && net.Password == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return rawURL
	}
	u.User = url.UserPassword(net.Username, net.Password)
	return u.String()
}

func setCookie(ctx context.Context, cookie Cookie, rawURL string) error {
	if cookie.Name == "" {
		return optionf("Cookies", "cookie name is empty")
	}
	params := network.SetCookieParams{Name: cookie.Name, Value: cookie.Value, URL: rawURL}
	if cookie.Domain != "" {
		params.Domain = cookie.Domain
	}
	if cookie.Path != "" {
		params.Path = cookie.Path
	}
	if cookie.Secure {
		params.Secure = &cookie.Secure
	}
	if cookie.HTTPOnly {
		params.HTTPOnly = &cookie.HTTPOnly
	}
	switch strings.ToLower(cookie.SameSite) {
	case "lax":
		params.SameSite = network.CookieSameSiteLax
	case "strict":
		params.SameSite = network.CookieSameSiteStrict
	case "none":
		params.SameSite = network.CookieSameSiteNone
	case "":
	default:
		return optionf("Cookies", "same site must be Lax, Strict, or None")
	}
	if _, err := chromedp.Call(ctx, network.SetCookie, params); err != nil {
		return fmt.Errorf("printkit: cookie %s: %w", cookie.Name, err)
	}
	return nil
}

func setTransparentBackground(ctx context.Context) error {
	type rgba struct {
		R int64   `json:"r"`
		G int64   `json:"g"`
		B int64   `json:"b"`
		A float64 `json:"a"`
	}
	type params struct {
		Color rgba `json:"color"`
	}
	// The generated RGBA type drops a zero alpha (omitzero), which Chromium
	// then treats as opaque. Send alpha explicitly.
	cmd := cdptype.Command[params, cdptype.Empty]{Method: "Emulation.setDefaultBackgroundColorOverride"}
	_, err := chromedp.Call(ctx, cmd, params{Color: rgba{A: 0}})
	if err != nil {
		return fmt.Errorf("printkit: transparent background: %w", err)
	}
	return nil
}

func addEarlyCSS(ctx context.Context, css string) error {
	payload, err := json.Marshal(css)
	if err != nil {
		return err
	}
	src := fmt.Sprintf(`(() => { const s = document.createElement('style'); s.setAttribute('data-printkit','css'); s.textContent = %s; (document.head || document.documentElement).appendChild(s); })()`, payload)
	_, err = chromedp.Call(ctx, cdppage.AddScriptToEvaluateOnNewDocument, cdppage.AddScriptToEvaluateOnNewDocumentParams{Source: src})
	if err != nil {
		return fmt.Errorf("printkit: inject css: %w", err)
	}
	return nil
}

func settle(ctx context.Context, wait Wait) error {
	idle := wait.NetworkIdle
	if wait.WaitUntil == WaitNetworkIdle && idle <= 0 {
		idle = 500 * time.Millisecond
	}
	if idle > 0 {
		if err := pollNetworkIdle(ctx, idle, pollTimeout(wait)); err != nil {
			return fmt.Errorf("printkit: network idle: %w", err)
		}
	}
	if wait.Selector != "" {
		if err := chromedp.Do(ctx, chromedp.WaitReady(chromedp.CSS(wait.Selector))); err != nil {
			return fmt.Errorf("printkit: wait for %s: %w", wait.Selector, err)
		}
	}
	if wait.WindowStatus != "" {
		expr := fmt.Sprintf("window.status === %s ? window.status : false", strconv.Quote(wait.WindowStatus))
		if _, err := chromedp.Run(ctx, chromedp.Poll[string](expr,
			chromedp.WithPollingInterval(50*time.Millisecond),
			chromedp.WithPollingTimeout(pollTimeout(wait)),
		)); err != nil {
			return fmt.Errorf("printkit: window status %q: %w", wait.WindowStatus, err)
		}
	}
	if wait.Delay > 0 {
		timer := time.NewTimer(wait.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func pollTimeout(wait Wait) time.Duration {
	if wait.Timeout > 0 {
		return wait.Timeout
	}
	return 30 * time.Second
}

func pollNetworkIdle(ctx context.Context, quiet, timeout time.Duration) error {
	ms := quiet.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	expr := fmt.Sprintf(`(() => {
		const quiet = %d;
		const now = performance.now();
		for (const e of performance.getEntriesByType('resource')) {
			if (!e.responseEnd || now - e.responseEnd < quiet) return false;
		}
		return document.readyState === 'complete';
	})()`, ms)
	_, err := chromedp.Run(ctx, chromedp.Poll[bool](expr,
		chromedp.WithPollingInterval(50*time.Millisecond),
		chromedp.WithPollingTimeout(timeout),
	))
	return err
}

func decorate(ctx context.Context, spec navSpec) error {
	css, js, err := spec.inject.combined()
	if err != nil {
		return err
	}
	if spec.grayscale {
		css += "\nhtml{filter:grayscale(1) !important;-webkit-filter:grayscale(1) !important;}"
	}
	if !boolOr(spec.network.Images, true) {
		css += "\nimg,picture,svg,video,canvas{visibility:hidden !important;}"
	}
	if css != "" {
		if err := evalJS(ctx, styleScript(css)); err != nil {
			return fmt.Errorf("printkit: inject css: %w", err)
		}
	}
	if spec.stripExternal || spec.stripInternal {
		if err := evalJS(ctx, stripLinksScript(spec.stripExternal, spec.stripInternal)); err != nil {
			return err
		}
	}
	if js != "" {
		if err := evalJS(ctx, js); err != nil {
			return fmt.Errorf("printkit: inject javascript: %w", err)
		}
	}
	if spec.minFontPx > 0 {
		if err := evalJS(ctx, minFontScript(spec.minFontPx)); err != nil {
			return fmt.Errorf("printkit: minimum font size: %w", err)
		}
	}
	return nil
}

func minFontScript(floorPx float64) string {
	return fmt.Sprintf(`(() => {
		const floor = %g;
		const root = document.body || document.documentElement;
		if (!root) return;
		const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT);
		let n = walker.currentNode;
		while (n) {
			const tag = n.tagName;
			if (tag !== 'SCRIPT' && tag !== 'STYLE' && tag !== 'NOSCRIPT') {
				const px = parseFloat(getComputedStyle(n).fontSize);
				if (px > 0 && px + 0.2 < floor) n.style.fontSize = floor + 'px';
			}
			n = walker.nextNode();
		}
	})()`, floorPx)
}

func styleScript(css string) string {
	payload, _ := json.Marshal(css)
	return fmt.Sprintf(`(() => { const s = document.createElement('style'); s.setAttribute('data-printkit','css'); s.textContent = %s; (document.head || document.documentElement).appendChild(s); })()`, payload)
}

func stripLinksScript(external, internal bool) string {
	return fmt.Sprintf(`(() => {
		const ext = %t, internal = %t;
		document.querySelectorAll('a[href]').forEach(a => {
			const h = a.getAttribute('href') || '';
			if (ext && /^https?:/i.test(h)) a.removeAttribute('href');
			if (internal && h.charAt(0) === '#') a.removeAttribute('href');
		});
	})()`, external, internal)
}

func evalJS(ctx context.Context, expr string) error {
	_, err := chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](expr))
	return err
}

func (inj Inject) combined() (string, string, error) {
	var css strings.Builder
	css.WriteString(inj.CSS)
	for _, name := range []string{inj.CSSFile, inj.UserStyleSheet} {
		if name == "" {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			return "", "", fmt.Errorf("printkit: read stylesheet %s: %w", name, err)
		}
		css.WriteByte('\n')
		css.Write(b)
	}
	js := inj.JavaScript
	if inj.JSFile != "" {
		b, err := os.ReadFile(inj.JSFile)
		if err != nil {
			return "", "", fmt.Errorf("printkit: read script %s: %w", inj.JSFile, err)
		}
		if js != "" {
			js = string(b) + "\n" + js
		} else {
			js = string(b)
		}
	}
	return css.String(), js, nil
}

func documentScrollWidth(ctx context.Context) (float64, error) {
	return chromedp.Run(ctx, chromedp.Evaluate[float64](
		`Math.max(document.documentElement ? document.documentElement.scrollWidth : 0, document.body ? document.body.scrollWidth : 0)`,
	))
}

func setDocumentTitle(ctx context.Context, title string) error {
	payload, err := json.Marshal(title)
	if err != nil {
		return err
	}
	return evalJS(ctx, "document.title = "+string(payload))
}
