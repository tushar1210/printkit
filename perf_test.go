package printkit_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tushar1210/printkit"
)

func TestStreamingSkipsPostProcess(t *testing.T) {
	var spans []string
	opts := append(chromeOpts(), printkit.WithTrace(func(name string, _ time.Duration) {
		spans = append(spans, name)
	}), printkit.WithPoolSize(1))
	c, err := printkit.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	off := false
	req := printkit.Request{
		Inputs:  []printkit.Input{printkit.FromHTML("<p>stream marker</p>")},
		Outline: printkit.Outline{Enabled: &off},
	}
	var buf bytes.Buffer
	if err := c.WritePDF(context.Background(), &buf, req); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Fatalf("prefix %q", buf.Bytes()[:min(8, buf.Len())])
	}
	if !containsSpan(spans, "printToPDF") || !containsSpan(spans, "transfer") {
		t.Fatalf("spans %v", spans)
	}
	if containsSpan(spans, "post") || containsSpan(spans, "outline") {
		t.Fatalf("post-processing ran: %v", spans)
	}
	text := pdfText(t, buf.Bytes())
	if !strings.Contains(text, "stream marker") {
		t.Fatalf("text %s", text)
	}
}

func TestTabRecycling(t *testing.T) {
	opts := append(chromeOpts(), printkit.WithPoolSize(1), printkit.WithMaxJobsPerTab(2))
	c, err := printkit.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	off := false
	req := printkit.Request{
		Inputs:  []printkit.Input{printkit.FromHTML("<p>recycle</p>")},
		Outline: printkit.Outline{Enabled: &off},
	}
	for i := 0; i < 3; i++ {
		if _, err := c.PDF(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	m := c.Metrics()
	if m.TabsOpened != 2 || m.TabsRetired != 1 {
		t.Fatalf("metrics %+v", m)
	}
}

func TestReadinessAndBlockRemote(t *testing.T) {
	ctx := context.Background()
	loaded, err := conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(`<p id="p">before</p><script>document.getElementById('p').textContent='after-load'</script>`)},
		Wait:   printkit.Wait{WaitUntil: printkit.WaitLoad},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, loaded), "after-load") {
		t.Fatalf("load wait:\n%s", pdfText(t, loaded))
	}
	none, err := conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(`<p>none-ready</p>`)},
		Wait:   printkit.Wait{WaitUntil: printkit.WaitNone},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, none), "none-ready") {
		t.Fatal(pdfText(t, none))
	}

	block := true
	remote, err := conv.PDF(ctx, printkit.Request{
		Inputs: []printkit.Input{printkit.FromHTML(`<p>LOCALTEXT</p><img src="http://example.invalid/nope.png">`)},
		Network: printkit.Network{
			BlockRemote: &block,
		},
		Wait: printkit.Wait{Timeout: 15 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdfText(t, remote), "LOCALTEXT") {
		t.Fatalf("blocked page:\n%s", pdfText(t, remote))
	}
}

func TestMemoryLimitRestartsBrowser(t *testing.T) {
	opts := append(chromeOpts(),
		printkit.WithPoolSize(1),
		printkit.WithMemoryLimit(1),
		printkit.WithMaxJobsPerTab(50),
	)
	c, err := printkit.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	off := false
	req := printkit.Request{
		Inputs:  []printkit.Input{printkit.FromHTML("<p>rss</p>")},
		Outline: printkit.Outline{Enabled: &off},
	}
	if _, err := c.PDF(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PDF(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if c.Metrics().BrowserRestarts < 1 {
		t.Fatalf("expected a restart, %+v", c.Metrics())
	}
}

func containsSpan(spans []string, name string) bool {
	for _, s := range spans {
		if s == name {
			return true
		}
	}
	return false
}
