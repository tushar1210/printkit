package printkit

import (
	"context"
	"fmt"
	"io"
	"time"

	cdio "github.com/chromedp/cdproto/io"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// readPDFStream reads a Page.printToPDF stream straight into w.
// The browser keeps the stream until IO.close, including after a failed read.
func readPDFStream(ctx context.Context, h cdio.StreamHandle, w io.Writer) error {
	if h == "" {
		return fmt.Errorf("printkit: chromium returned an empty pdf")
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = chromedp.Call(cctx, cdio.Close, cdio.CloseParams{Handle: h})
	}()
	var prefix []byte
	wrote := 0
	for {
		res, err := chromedp.Call(ctx, cdio.Read, cdio.ReadParams{Handle: h, Size: 1 << 20})
		if err != nil {
			return fmt.Errorf("printkit: print pdf: %w", err)
		}
		chunk := res.Data
		if len(prefix) < 5 && len(chunk) > 0 {
			need := 5 - len(prefix)
			if need > len(chunk) {
				need = len(chunk)
			}
			prefix = append(prefix, chunk[:need]...)
			if len(prefix) >= 5 && string(prefix[:5]) != "%PDF-" {
				return fmt.Errorf("printkit: chromium returned an empty pdf")
			}
		}
		if len(chunk) > 0 {
			if _, err := w.Write(chunk); err != nil {
				return fmt.Errorf("printkit: write pdf: %w", err)
			}
			wrote += len(chunk)
		}
		if res.EOF {
			break
		}
	}
	if wrote < 5 {
		return fmt.Errorf("printkit: chromium returned an empty pdf")
	}
	return nil
}

// writePrint calls Page.printToPDF with transferMode ReturnAsStream and copies
// the bytes into w. It does not assemble a base64 document in memory.
func (c *Converter) writePrint(ctx context.Context, w io.Writer, opts []chromedp.PDFOption) error {
	var params cdppage.PrintToPDFParams
	for _, opt := range opts {
		opt(&params)
	}
	params.TransferMode = cdppage.PrintToPDFTransferModeReturnAsStream
	stopPrint := traceDone(c.cfg.trace, "printToPDF")
	res, err := chromedp.Call(ctx, cdppage.PrintToPDF, params)
	stopPrint()
	if err != nil {
		return fmt.Errorf("printkit: print pdf: %w", err)
	}
	stopTransfer := traceDone(c.cfg.trace, "transfer")
	err = readPDFStream(ctx, res.Stream, w)
	stopTransfer()
	return err
}
