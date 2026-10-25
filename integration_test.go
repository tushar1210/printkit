package printkit_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"golang.org/x/image/webp"

	"github.com/tushar-singh-dev/printkit"
	"github.com/tushar-singh-dev/printkit/internal/cli"
)

var conv *printkit.Converter

func TestMain(m *testing.M) {
	if os.Getenv("PRINTKIT_NO_SANDBOX") == "" && os.Geteuid() != 0 {
		probe, err := printkit.New(printkit.WithDefaultTimeout(20 * time.Second))
		if err != nil {
			_ = os.Setenv("PRINTKIT_NO_SANDBOX", "1")
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			_, err = probe.PDF(ctx, printkit.Request{Inputs: []printkit.Input{printkit.FromHTML("<p>ok</p>")}})
			cancel()
			_ = probe.Close()
			if err != nil {
				_ = os.Setenv("PRINTKIT_NO_SANDBOX", "1")
			}
		}
	}
	opts := []printkit.Option{
		printkit.WithPoolSize(3),
		printkit.WithDefaultTimeout(45 * time.Second),
	}
	if os.Getenv("PRINTKIT_NO_SANDBOX") == "1" || os.Geteuid() == 0 {
		opts = append(opts, printkit.WithNoSandbox(true))
	}
	c, err := printkit.New(opts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	conv = c
	code := m.Run()
	_ = c.Close()
	os.Exit(code)
}

func chromeOpts() []printkit.Option {
	if os.Getenv("PRINTKIT_NO_SANDBOX") == "1" || os.Geteuid() == 0 {
		return []printkit.Option{printkit.WithNoSandbox(true)}
	}
	return nil
}

const multiHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Sample Doc</title>
<style>h1,h2,p{font-family:sans-serif} .break{break-after:page}</style></head><body>
<h1>Alpha Chapter</h1><p>Hello unique alpha marker</p><div class="break"></div>
<h2>Beta Section</h2><p>Hello unique beta marker</p><div class="break"></div>
<h1>Gamma End</h1><p>Hello unique gamma marker</p>
</body></html>`

func TestPDFTextPagesAndOutline(t *testing.T) {
	pdf, err := conv.PDF(context.Background(), printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(multiHTML)},
		Header: &printkit.HeaderFooter{Left: "ABCHEADER", Center: "[page]/[topage]"},
		Footer: &printkit.HeaderFooter{Center: "XYZFOOTER"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := pages(t, pdf); n != 3 {
		t.Fatalf("pages = %d", n)
	}
	text := pdfText(t, pdf)
	for _, want := range []string{"ABCHEADER", "XYZFOOTER", "Hello unique alpha marker", "Hello unique beta marker", "Hello unique gamma marker", "1/3", "3/3"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	titles := bookmarkTitles(t, pdf)
	for _, want := range []string{"Alpha Chapter", "Beta Section", "Gamma End"} {
		if !titles[want] {
			t.Fatalf("outline missing %q: %v", want, titles)
		}
	}
}

func TestPDFPageSize(t *testing.T) {
	a4, err := conv.PDF(context.Background(), printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML("<p>size</p>")},
		Page:   printkit.Page{Size: printkit.A4},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, h := pageDim(t, a4)
	assertNear(t, w, 210.0/25.4*72, 2)
	assertNear(t, h, 297.0/25.4*72, 2)

	letter, err := conv.PDF(context.Background(), printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML("<p>size</p>")},
		Page:   printkit.Page{Size: printkit.Letter, Orientation: printkit.Landscape},
	})
	if err != nil {
		t.Fatal(err)
	}
	w, h = pageDim(t, letter)
	assertNear(t, w, 11*72, 2)
	assertNear(t, h, 8.5*72, 2)
}

func TestPDFMetadataCoverRangesAndTOC(t *testing.T) {
	ctx := context.Background()
	ranged, err := conv.PDF(ctx, printkit.Request{
		Inputs:     []printkit.Input{printkit.FromHTML(multiHTML)},
		PageRanges: "1,3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := pages(t, ranged); n != 2 {
		t.Fatalf("ranged pages = %d", n)
	}
	text := pdfText(t, ranged)
	if strings.Contains(text, "beta marker") || !strings.Contains(text, "alpha marker") || !strings.Contains(text, "gamma marker") {
		t.Fatalf("range text:\n%s", text)
	}

	meta, err := conv.PDF(ctx, printkit.Request{
		Inputs:   []printkit.Input{printkit.FromHTML("<h1>Body</h1>")},
		Metadata: printkit.Metadata{Title: "Quarterly Review", Author: "Ada Lovelace"},
	})
	if err != nil {
		t.Fatal(err)
	}
	info := pdfInfo(t, meta)
	if !strings.Contains(info, "Quarterly Review") || !strings.Contains(info, "Ada Lovelace") {
		t.Fatalf("pdfinfo:\n%s", info)
	}

	covered, err := conv.PDF(ctx, printkit.Request{
		Cover:  &printkit.Input{HTML: "<h1>COVERPAGE</h1>"},
		Inputs: []printkit.Input{printkit.FromHTML("<h1>Body Chapter</h1><p>body text</p>")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := pages(t, covered); n < 2 {
		t.Fatalf("cover pages = %d", n)
	}
	if !strings.Contains(pdfText(t, covered), "COVERPAGE") {
		t.Fatal(pdfText(t, covered))
	}
	if !bookmarkTitles(t, covered)["Cover"] {
		t.Fatalf("cover outline %v", bookmarkTitles(t, covered))
	}

	tocPDF, err := conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(multiHTML)},
		TOC:    &printkit.TOC{Title: "Contents"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pages(t, tocPDF) < 4 {
		t.Fatalf("toc pages = %d", pages(t, tocPDF))
	}
	tocText := pdfText(t, tocPDF)
	if !strings.Contains(tocText, "Contents") || !strings.Contains(tocText, "Alpha Chapter") {
		t.Fatalf("toc text:\n%s", tocText)
	}
	if !bookmarkTitles(t, tocPDF)["Contents"] {
		t.Fatalf("toc outline %v", bookmarkTitles(t, tocPDF))
	}
}

func TestPDFReaderMergeFileAccessAndJS(t *testing.T) {
	ctx := context.Background()
	merged, err := conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{
			printkit.FromReader(strings.NewReader("<p>FIRSTPART</p>")),
			printkit.FromHTML("<p>SECONDPART</p>"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := pdfText(t, merged)
	if !strings.Contains(text, "FIRSTPART") || !strings.Contains(text, "SECONDPART") {
		t.Fatalf("merge:\n%s", text)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "local.html")
	if err := os.WriteFile(path, []byte("<p>FILEOK</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	off := false
	if _, err := conv.PDF(ctx, printkit.Request{
		Inputs:  []printkit.Input{printkit.FromFile(path)},
		Network: printkit.Network{AllowFileAccess: &off},
	}); err == nil || !strings.Contains(err.Error(), "file access") {
		t.Fatalf("file access: %v", err)
	}
	ok, err := conv.PDF(ctx, printkit.Request{Inputs: []printkit.Input{printkit.FromFile(path)}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, ok), "FILEOK") {
		t.Fatal(pdfText(t, ok))
	}

	jsOn, err := conv.PDF(ctx, printkit.Request{Inputs: []printkit.Input{printkit.FromHTML(
		`<p id="t">BEFORE</p><script>document.getElementById('t').textContent='AFTERJS'</script>`,
	)}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, jsOn), "AFTERJS") {
		t.Fatalf("js on:\n%s", pdfText(t, jsOn))
	}
	jsOff, err := conv.PDF(ctx, printkit.Request{
		Inputs:  []printkit.Input{printkit.FromHTML(`<p id="t">BEFORE</p><script>document.getElementById('t').textContent='AFTERJS'</script>`)},
		Network: printkit.Network{JavaScript: &off},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := pdfText(t, jsOff)
	if strings.Contains(got, "AFTERJS") || !strings.Contains(got, "BEFORE") {
		t.Fatalf("js off:\n%s", got)
	}

	injected, err := conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(`<p id="t">OLD</p>`)},
		Inject: printkit.Inject{JavaScript: `document.getElementById('t').textContent='INJECTED'`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, injected), "INJECTED") {
		t.Fatal(pdfText(t, injected))
	}
}

func TestPDFWaitAndMedia(t *testing.T) {
	ctx := context.Background()
	ready, err := conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(`<p id="t">WAIT</p><script>setTimeout(function(){window.status='ready'; document.getElementById('t').textContent='STATUSREADY'}, 30)</script>`)},
		Wait:   printkit.Wait{WindowStatus: "ready", Selector: "#t"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, ready), "STATUSREADY") {
		t.Fatal(pdfText(t, ready))
	}
	ctxTimeout, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err = conv.PDF(ctxTimeout, printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(`<p>never</p>`)},
		Wait:   printkit.Wait{WindowStatus: "never", Timeout: time.Second},
	})
	if err == nil {
		t.Fatal("expected window status to time out")
	}
	_, err = conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(`<p>nope</p>`)},
		Wait:   printkit.Wait{Selector: "#missing", Timeout: time.Second},
	})
	if err == nil {
		t.Fatal("expected missing selector to fail")
	}

	mediaPage := `<p id="m">X</p><script>document.getElementById('m').textContent = window.matchMedia('print').matches ? 'PRINTMEDIA' : 'SCREENMEDIA'</script>`
	printed, err := conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(mediaPage)},
		Page:   printkit.Page{Media: printkit.MediaPrint},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, printed), "PRINTMEDIA") {
		t.Fatalf("print media:\n%s", pdfText(t, printed))
	}
	screen, err := conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(mediaPage)},
		Page:   printkit.Page{Media: printkit.MediaScreen},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, screen), "SCREENMEDIA") {
		t.Fatalf("screen media:\n%s", pdfText(t, screen))
	}
}

func TestPDFNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "ada" || pass != "secret" {
				w.Header().Set("WWW-Authenticate", `Basic realm="printkit"`)
				http.Error(w, "nope", http.StatusUnauthorized)
				return
			}
			_, _ = fmt.Fprint(w, "<p>AUTHOK</p>")
			return
		}
		if r.Header.Get("X-Printkit") != "yes" {
			http.Error(w, "header", http.StatusForbidden)
			return
		}
		if c, err := r.Cookie("session"); err != nil || c.Value != "abc" {
			http.Error(w, "cookie", http.StatusForbidden)
			return
		}
		_, _ = fmt.Fprint(w, "<p>HEADERCOOKIE</p>")
	}))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	pdf, err := conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{printkit.FromURL(srv.URL + "/doc")},
		Network: printkit.Network{
			Headers: map[string]string{"X-Printkit": "yes"},
			Cookies: []printkit.Cookie{{Name: "session", Value: "abc"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, pdf), "HEADERCOOKIE") {
		t.Fatal(pdfText(t, pdf))
	}
	authed, err := conv.PDF(ctx, printkit.Request{
		Inputs:  []printkit.Input{printkit.FromURL(srv.URL + "/auth")},
		Network: printkit.Network{Username: "ada", Password: "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, authed), "AUTHOK") {
		t.Fatal(pdfText(t, authed))
	}
	_, err = conv.PDF(ctx, printkit.Request{Inputs: []printkit.Input{printkit.FromURL(srv.URL + "/missing")}})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
}

func TestImages(t *testing.T) {
	ctx := context.Background()
	html := `<!doctype html><html><head><style>html,body{margin:0;background:#ff0000}</style></head><body></body></html>`
	pngBytes, err := conv.Image(ctx, printkit.ImageRequest{
		Input: printkit.FromHTML(html), Format: printkit.PNG, Width: 40, Height: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pngBytes, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("png magic %x", pngBytes[:8])
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 40 || cfg.Height != 30 {
		t.Fatalf("png size %dx%d", cfg.Width, cfg.Height)
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(5, 5).RGBA()
	if r>>8 < 200 || g>>8 > 40 || b>>8 > 40 {
		t.Fatalf("color pixel r=%d g=%d b=%d", r>>8, g>>8, b>>8)
	}

	gray, err := conv.Image(ctx, printkit.ImageRequest{
		Input: printkit.FromHTML(html), Format: printkit.PNG, Width: 40, Height: 30, Grayscale: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	gimg, err := png.Decode(bytes.NewReader(gray))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ = gimg.At(5, 5).RGBA()
	rr, gg, bb := int(r>>8), int(g>>8), int(b>>8)
	if abs(rr-gg) > 12 || abs(gg-bb) > 12 || rr < 20 {
		t.Fatalf("gray pixel %d %d %d", rr, gg, bb)
	}

	jpg, err := conv.Image(ctx, printkit.ImageRequest{
		Input: printkit.FromHTML(html), Format: printkit.JPEG, Quality: 80, Width: 40, Height: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(jpg, []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("jpeg magic %x", jpg[:4])
	}
	jcfg, err := jpeg.DecodeConfig(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	if jcfg.Width != 40 || jcfg.Height != 30 {
		t.Fatalf("jpeg %dx%d", jcfg.Width, jcfg.Height)
	}

	web, err := conv.Image(ctx, printkit.ImageRequest{
		Input: printkit.FromHTML(html), Format: printkit.WEBP, Quality: 80, Width: 32, Height: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(web) < 12 || string(web[:4]) != "RIFF" || string(web[8:12]) != "WEBP" {
		t.Fatalf("webp magic %x", web[:min(12, len(web))])
	}
	wcfg, err := webp.DecodeConfig(bytes.NewReader(web))
	if err != nil {
		t.Fatal(err)
	}
	if wcfg.Width != 32 || wcfg.Height != 24 {
		t.Fatalf("webp %dx%d", wcfg.Width, wcfg.Height)
	}

	full, err := conv.Image(ctx, printkit.ImageRequest{
		Input: printkit.FromHTML(`<div style="height:2000px;background:#00f">tall</div>`),
		Width: 200, FullPage: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fcfg, _, err := image.DecodeConfig(bytes.NewReader(full))
	if err != nil {
		t.Fatal(err)
	}
	if fcfg.Height < 1500 {
		t.Fatalf("full page height %d", fcfg.Height)
	}
}

func TestConcurrentAndClosed(t *testing.T) {
	var wg sync.WaitGroup
	errCh := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			pdf, err := conv.PDF(context.Background(), printkit.Request{
				Inputs: []printkit.Input{printkit.FromHTML(fmt.Sprintf("<p>JOB%d</p>", n))},
			})
			if err != nil {
				errCh <- err
				return
			}
			cmd := exec.Command("pdftotext", "-layout", "-", "-")
			cmd.Stdin = bytes.NewReader(pdf)
			out, err := cmd.Output()
			if err != nil {
				errCh <- err
				return
			}
			if !strings.Contains(string(out), fmt.Sprintf("JOB%d", n)) {
				errCh <- fmt.Errorf("job %d text missing", n)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	one, err := printkit.New(append(chromeOpts(), printkit.WithPoolSize(1))...)
	if err != nil {
		t.Fatal(err)
	}
	_ = one.Close()
	_, err = one.PDF(context.Background(), printkit.Request{Inputs: []printkit.Input{printkit.FromHTML("<p>x</p>")}})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed: %v", err)
	}
}

func TestCLIEndToEnd(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.html")
	out := filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(in, []byte("<h1>CLI Chapter</h1><p>CLIMARKER</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--header-left", "CLIHEADER", "--title", "From CLI", in, out}
	if os.Getenv("PRINTKIT_NO_SANDBOX") == "1" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	var stderr bytes.Buffer
	code := cli.Run("htmlprint", args, ioDiscard{}, &stderr)
	if code != 0 {
		t.Fatalf("cli exit %d: %s", code, stderr.String())
	}
	pdf, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := pdfText(t, pdf)
	if !strings.Contains(text, "CLIMARKER") || !strings.Contains(text, "CLIHEADER") {
		t.Fatalf("cli text:\n%s", text)
	}
}

func ExampleConverter_PDF() {
	opts := chromeOpts()
	conv, err := printkit.New(opts...)
	if err != nil {
		panic(err)
	}
	defer func() { _ = conv.Close() }()
	pdf, err := conv.PDF(context.Background(), printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML("<h1>Report</h1><p>Hello</p>")},
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s\n", pdf[:5])
	// Output:
	// %PDF-
}

func pages(t *testing.T, pdf []byte) int {
	t.Helper()
	n, err := api.PageCount(context.Background(), bytes.NewReader(pdf), nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func pageDim(t *testing.T, pdf []byte) (float64, float64) {
	t.Helper()
	dims, err := api.PageDims(context.Background(), bytes.NewReader(pdf), nil)
	if err != nil || len(dims) == 0 {
		t.Fatal(err)
	}
	return dims[0].Width, dims[0].Height
}

func bookmarkTitles(t *testing.T, pdf []byte) map[string]bool {
	t.Helper()
	bms, err := api.Bookmarks(context.Background(), bytes.NewReader(pdf), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	var walk func([]pdfcpu.Bookmark)
	walk = func(in []pdfcpu.Bookmark) {
		for _, b := range in {
			out[b.Title] = true
			walk(b.Kids)
		}
	}
	walk(bms)
	return out
}

func pdfInfo(t *testing.T, pdf []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(path, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("pdfinfo", path).Output()
	if err != nil {
		t.Fatalf("pdfinfo: %v", err)
	}
	return string(out)
}

func pdfText(t *testing.T, pdf []byte) string {
	t.Helper()
	cmd := exec.Command("pdftotext", "-layout", "-", "-")
	cmd.Stdin = bytes.NewReader(pdf)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	return string(out)
}

func assertNear(t *testing.T, got, want, tol float64) {
	t.Helper()
	if got < want-tol || got > want+tol {
		t.Fatalf("got %v want %v ± %v", got, want, tol)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
