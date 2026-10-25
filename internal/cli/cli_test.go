package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/tushar-singh-dev/printkit"
)

func TestParsePDFDefaults(t *testing.T) {
	cmd, err := Parse("wkhtmltopdf", []string{
		"--margin-top", "0",
		"--header-center", "[page]/[topage]",
		"--footer-right", "[section]",
		"--replace", "name", "Ada",
		"cover", "cover.html",
		"toc",
		"page", "body.html",
		"out.pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Mode != "pdf" || cmd.Output != "out.pdf" {
		t.Fatalf("mode %s output %s", cmd.Mode, cmd.Output)
	}
	if cmd.PDF.Cover == nil || cmd.PDF.Cover.File != "cover.html" {
		t.Fatalf("cover %#v", cmd.PDF.Cover)
	}
	if cmd.PDF.TOC == nil || len(cmd.PDF.Inputs) != 1 || cmd.PDF.Inputs[0].File != "body.html" {
		t.Fatalf("inputs %#v toc %#v", cmd.PDF.Inputs, cmd.PDF.TOC)
	}
	if !cmd.PDF.Page.Margin.Custom() || cmd.PDF.Page.Margin.Top.Value != 0 {
		t.Fatalf("margin %#v", cmd.PDF.Page.Margin)
	}
	if cmd.PDF.Page.Margin.Bottom.Millimeters() != 10 {
		t.Fatalf("default side %#v", cmd.PDF.Page.Margin.Bottom)
	}
	if !cmd.PDF.Page.SmartShrinking || cmd.PDF.Page.Media != printkit.MediaScreen {
		t.Fatalf("page %#v", cmd.PDF.Page)
	}
	if cmd.PDF.Page.PrintBackground == nil || *cmd.PDF.Page.PrintBackground {
		t.Fatal("CLI backgrounds should default off")
	}
	if cmd.PDF.Header == nil || cmd.PDF.Header.Center != "[page]/[topage]" {
		t.Fatalf("header %#v", cmd.PDF.Header)
	}
	if cmd.PDF.Header.Replace["name"] != "Ada" || cmd.PDF.Footer.Right != "[section]" {
		t.Fatalf("footer %#v", cmd.PDF.Footer)
	}
}

func TestParseImageAndModeSwitch(t *testing.T) {
	cmd, err := Parse("htmlprint", []string{
		"image", "--format", "webp", "--quality", "80", "--width", "640", "--height", "480",
		"--crop-x", "1", "--crop-y", "2", "--crop-w", "30", "--crop-h", "40",
		"--disable-smart-width", "https://example.test/", "out.webp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Mode != "image" || cmd.Image.Format != printkit.WEBP || cmd.Image.Quality != 80 {
		t.Fatalf("%#v", cmd.Image)
	}
	if cmd.Image.Input.URL != "https://example.test/" || cmd.Image.SmartWidth {
		t.Fatalf("%#v", cmd.Image)
	}
	if cmd.Image.Clip == nil || cmd.Image.Clip.Width != 30 || cmd.Image.Clip.Height != 40 {
		t.Fatalf("clip %#v", cmd.Image.Clip)
	}
	imgDefault, err := Parse("wkhtmltoimage", []string{"page.html", "out.png"})
	if err != nil {
		t.Fatal(err)
	}
	if imgDefault.Mode != "image" || !imgDefault.Image.SmartWidth || imgDefault.Image.Input.File != "page.html" {
		t.Fatalf("%#v", imgDefault)
	}
}

func TestParseRejectsUnsupported(t *testing.T) {
	_, err := Parse("htmlprint", []string{"--copies", "2", "a.html", "out.pdf"})
	if err == nil || !strings.Contains(err.Error(), "unsupported flag --copies") {
		t.Fatalf("copies: %v", err)
	}
	_, err = Parse("htmlprint", []string{"--not-a-flag", "a.html", "out.pdf"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unknown: %v", err)
	}
	_, err = Parse("htmlprint", []string{"--encoding", "latin1", "a.html", "out.pdf"})
	if err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("encoding: %v", err)
	}
	_, err = Parse("htmlprint", []string{"image", "--page-size", "A4", "a.html", "out.png"})
	if err == nil || !strings.Contains(err.Error(), "not valid for image") {
		t.Fatalf("mode: %v", err)
	}
}

func TestParseNetworkAndWait(t *testing.T) {
	cmd, err := Parse("htmlprint", []string{
		"--print-media-type", "--background", "--disable-smart-shrinking",
		"--custom-header", "X-Printkit", "yes",
		"--cookie", "session", "abc",
		"--username", "ada", "--password", "secret",
		"--javascript-delay", "250", "--window-status", "ready",
		"--wait-for", "#done", "--wait-network-idle", "400",
		"--timeout", "15", "--disable-local-file-access",
		"--page-size", "Legal", "--orientation", "Landscape", "--zoom", "1.25",
		"https://example.test/doc", "-",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := cmd.PDF
	if req.Page.Media != printkit.MediaPrint || req.Page.SmartShrinking || req.Page.Size != printkit.Legal {
		t.Fatalf("page %#v", req.Page)
	}
	if req.Page.Orientation != printkit.Landscape || req.Page.Scale != 1.25 {
		t.Fatalf("page %#v", req.Page)
	}
	if req.Page.PrintBackground == nil || !*req.Page.PrintBackground {
		t.Fatal("background")
	}
	if req.Network.Headers["X-Printkit"] != "yes" || len(req.Network.Cookies) != 1 || req.Network.Cookies[0].Value != "abc" {
		t.Fatalf("network %#v", req.Network)
	}
	if req.Network.Username != "ada" || req.Network.AllowFileAccess == nil || *req.Network.AllowFileAccess {
		t.Fatalf("network %#v", req.Network)
	}
	if req.Wait.Delay.Milliseconds() != 250 || req.Wait.WindowStatus != "ready" || req.Wait.Selector != "#done" {
		t.Fatalf("wait %#v", req.Wait)
	}
	if req.Wait.NetworkIdle.Milliseconds() != 400 || req.Wait.Timeout.Seconds() != 15 {
		t.Fatalf("wait %#v", req.Wait)
	}
	if cmd.Output != "-" || req.Inputs[0].URL != "https://example.test/doc" {
		t.Fatalf("output %s input %#v", cmd.Output, req.Inputs)
	}
}

func TestHelpDoesNotLaunchChrome(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := Run("htmlprint", []string{"--help"}, &out, &errBuf)
	if code != 0 || !strings.Contains(out.String(), "htmlprint renders HTML") {
		t.Fatalf("code %d out %q err %q", code, out.String(), errBuf.String())
	}
	code = Run("htmlprint", []string{"--version"}, io.Discard, io.Discard)
	if code != 0 {
		t.Fatal(code)
	}
	var stderr bytes.Buffer
	code = Run("htmlprint", []string{"--copies", "2", "a.html", "b.pdf"}, io.Discard, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "--copies") {
		t.Fatalf("code %d %s", code, stderr.String())
	}
}
