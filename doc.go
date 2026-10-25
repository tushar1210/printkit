// Package printkit renders HTML to PDF and images with headless Chromium.
//
// wkhtmltopdf and wkhtmltoimage are archived and render modern CSS through
// QtWebKit. printkit drives a current Chromium build over the Chrome DevTools
// Protocol (via github.com/chromedp/chromedp) and uses Page.printToPDF and
// Page.captureScreenshot. Merging, outlines, metadata, and page selection are
// done in pure Go with github.com/pdfcpu/pdfcpu, so the module does not need
// CGo.
//
// A [Converter] owns one Chromium process and a pool of tabs. It is safe for
// concurrent use. Call [Converter.Close] when the process should exit.
//
//	conv, err := printkit.New()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer conv.Close()
//
//	pdf, err := conv.PDF(ctx, printkit.Request{
//	    Inputs: []printkit.Input{printkit.FromHTML("<h1>Report</h1>")},
//	    Page:   printkit.Page{Size: printkit.A4},
//	})
//
// Pass a local file with [FromFile], a remote document with [FromURL], or a
// stream with [FromReader]. Several inputs are merged, in order, into one PDF.
// A cover and a generated table of contents are optional.
//
// The command in cmd/htmlprint accepts the wkhtmltopdf and wkhtmltoimage flags
// that still matter for scripts. See the README for the flag map and for the
// flags that are intentionally unsupported.
package printkit
