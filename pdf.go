package printkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"strings"
	"sync"
	"time"

	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
)

func (c *Converter) renderPDF(ctx context.Context, req Request) ([]byte, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The cover does not need the body. TOC page numbers do, so the contents
	// stay after the body inside renderSingle / renderMany.
	type pieceResult struct {
		pdf   []byte
		marks []pdfcpu.Bookmark
		err   error
	}
	var coverCh chan pieceResult
	if req.Cover != nil {
		coverCh = make(chan pieceResult, 1)
		go func() {
			coverReq := withInput(req, *req.Cover, true)
			coverReq.TOC = nil
			cover, err := c.renderSingle(ctx, *req.Cover, coverReq)
			if errors.Is(err, errSkipInput) {
				err = ErrNoInput
			}
			coverCh <- pieceResult{pdf: cover.pdf, err: err}
		}()
	}
	var body pieceResult
	switch len(req.Inputs) {
	case 0:
	case 1:
		renderedBody, err := c.renderSingle(ctx, req.Inputs[0], req)
		if errors.Is(err, errSkipInput) {
			err = ErrNoInput
		}
		body = pieceResult{pdf: renderedBody.pdf, marks: renderedBody.marks, err: err}
	default:
		renderedBody, err := c.renderMany(ctx, req)
		body = pieceResult{pdf: renderedBody.pdf, marks: renderedBody.marks, err: err}
	}
	if body.err != nil {
		cancel()
	}
	var pieces [][]byte
	var marks []pdfcpu.Bookmark
	if coverCh != nil {
		cr := <-coverCh
		if body.err != nil {
			if errors.Is(body.err, context.Canceled) && cr.err != nil && !errors.Is(cr.err, context.Canceled) {
				return nil, cr.err
			}
			return nil, body.err
		}
		if cr.err != nil {
			return nil, cr.err
		}
		pieces = append(pieces, cr.pdf)
	} else if body.err != nil {
		return nil, body.err
	}
	if body.pdf != nil {
		pieces = append(pieces, body.pdf)
		marks = body.marks
	}
	if len(pieces) == 0 {
		return nil, ErrNoInput
	}
	if len(pieces) == 1 && !requestNeedsPost(req) {
		return pieces[0], nil
	}
	stopPost := traceDone(c.cfg.trace, "post")
	defer stopPost()
	out, err := mergePDFs(pieces)
	if err != nil {
		return nil, err
	}
	if req.Cover != nil && len(pieces) == 2 {
		coverN, err := pdfPageCount(pieces[0])
		if err != nil {
			return nil, err
		}
		if marks == nil {
			marks, err = pdfBookmarks(pieces[1])
			if err != nil {
				return nil, err
			}
		}
		marks = shiftBookmarks(marks, coverN)
		marks = append([]pdfcpu.Bookmark{{Title: "Cover", PageFrom: 1}}, marks...)
	}
	if marks == nil && (req.Cover != nil || len(req.Inputs) != 1) {
		marks, err = pdfBookmarks(out)
		if err != nil {
			return nil, err
		}
	}
	out, err = applyOutline(out, req, marks)
	if err != nil {
		return nil, err
	}
	out, err = setMetadata(out, req.Metadata)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.PageRanges) != "" {
		before, err := pdfPageCount(out)
		if err != nil {
			return nil, err
		}
		kept, err := parsePageRanges(req.PageRanges, before)
		if err != nil {
			return nil, err
		}
		pre, err := pdfBookmarks(out)
		if err != nil {
			return nil, err
		}
		out, err = trimPDF(out, req.PageRanges)
		if err != nil {
			return nil, err
		}
		if req.Outline.enabled() {
			oldToNew := map[int]int{}
			for i, p := range kept {
				oldToNew[p] = i + 1
			}
			mapped := filterDepth(remapBookmarks(pre, oldToNew), req.Outline.depth())
			if len(mapped) == 0 {
				out, err = clearBookmarks(out)
			} else {
				out, err = setBookmarks(out, mapped)
			}
			if err != nil {
				return nil, err
			}
		}
	}
	if req.Copies > 1 {
		out, err = repeatPDF(out, req.Copies, boolOr(req.Collate, true))
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

type rendered struct {
	pdf   []byte
	marks []pdfcpu.Bookmark
}

type plainOpts struct {
	nativeHeader  bool
	reserveHeader bool
}

func validateRequest(req Request) error {
	if len(req.Inputs) == 0 && req.Cover == nil {
		return ErrNoInput
	}
	switch req.LoadErrors {
	case "", LoadErrorAbort, LoadErrorIgnore, LoadErrorSkip:
	default:
		return optionf("LoadErrors", "must be abort, ignore, or skip")
	}
	if req.Copies < 0 {
		return optionf("Copies", "must be zero or positive")
	}
	if req.MinimumFontSize < 0 {
		return optionf("MinimumFontSize", "must be zero or positive")
	}
	_, err := req.Page.box(headerActive(req.Header), headerActive(req.Footer))
	return err
}

func withInput(req Request, in Input, cover bool) Request {
	out := req
	if in.Page != nil {
		out.Page = *in.Page
	}
	if cover {
		if in.Header == nil {
			out.Header = nil
		}
		if in.Footer == nil {
			out.Footer = nil
		}
	}
	if in.Header != nil {
		if headerActive(in.Header) {
			h := *in.Header
			out.Header = &h
		} else {
			out.Header = nil
		}
	}
	if in.Footer != nil {
		if headerActive(in.Footer) {
			f := *in.Footer
			out.Footer = &f
		} else {
			out.Footer = nil
		}
	}
	return out
}

func heterogeneous(req Request) bool {
	for _, in := range req.Inputs {
		if in.Page != nil || in.Header != nil || in.Footer != nil {
			return true
		}
	}
	return false
}

func (c *Converter) renderSingle(ctx context.Context, in Input, req Request) (rendered, error) {
	req = withInput(req, in, false)
	if req.TOC != nil && req.TOC.Page != nil {
		return c.renderSeparateTOC(ctx, in, req)
	}
	hasHeader := headerActive(req.Header)
	hasFooter := headerActive(req.Footer)
	box, err := req.Page.box(hasHeader, hasFooter)
	if err != nil {
		return rendered{}, err
	}
	box = applySpacing(box, req.Header, req.Footer)
	headerStatic, err := needsStatic(req.Header, req.PageOffset, false)
	if err != nil {
		return rendered{}, err
	}
	footerStatic, err := needsStatic(req.Footer, req.PageOffset, false)
	if err != nil {
		return rendered{}, err
	}
	static := headerStatic || footerStatic
	var out rendered
	err = c.withTab(ctx, req.Network.Proxy, func(tab context.Context) error {
		tab, cancel := bound(tab, req.Wait.Timeout, req.Network.Timeout)
		defer cancel()
		info, cleanup, err := c.open(tab, navFor(in, req))
		if err != nil {
			return err
		}
		defer cleanup()
		if req.Metadata.Title != "" {
			if err := setDocumentTitle(tab, req.Metadata.Title); err != nil {
				return err
			}
			info.Title = req.Metadata.Title
		}
		scale, err := c.scaleFor(tab, req, box)
		if err != nil {
			return err
		}
		if req.TOC != nil {
			if err := evalJS(tab, ensureHeadingScript); err != nil {
				return err
			}
			pass1, err := c.printPDF(tab, box, scale, req, info, false)
			if err != nil {
				return err
			}
			items, err := tocItemsFrom(tab, pass1, req.TOC.depth())
			if err != nil {
				return err
			}
			estimate := estimateTOCPages(len(items), box)
			frag, err := tocFragment(req.TOC, items, estimate, req.PageOffset)
			if err != nil {
				return err
			}
			if err := injectTOC(tab, frag); err != nil {
				return err
			}
			pdf, err := c.printPDF(tab, box, scale, req, info, !static)
			if err != nil {
				return err
			}
			n1, err := pdfPageCount(pass1)
			if err != nil {
				return err
			}
			n2, err := pdfPageCount(pdf)
			if err != nil {
				return err
			}
			shift := n2 - n1
			if shift < 1 {
				shift = 1
			}
			if shift != estimate {
				frag, err := tocFragment(req.TOC, items, shift, req.PageOffset)
				if err != nil {
					return err
				}
				if err := injectTOC(tab, frag); err != nil {
					return err
				}
				pdf, err = c.printPDF(tab, box, scale, req, info, !static)
				if err != nil {
					return err
				}
			}
			if static && (hasHeader || hasFooter) {
				pdf, err = c.overlayBands(tab, pdf, req, info, box, nil, 0, 0)
				if err != nil {
					return err
				}
			}
			bms, err := pdfBookmarks(pdf)
			if err != nil {
				return err
			}
			bms = append([]pdfcpu.Bookmark{{Title: req.TOC.title(), PageFrom: 1}}, bms...)
			pdf, err = setBookmarks(pdf, filterDepth(bms, req.Outline.depth()))
			if err != nil {
				return err
			}
			out = rendered{pdf: pdf, marks: bms}
			return nil
		}
		native := !static && (hasHeader || hasFooter)
		pdf, err := c.printPDF(tab, box, scale, req, info, native)
		if err != nil {
			return err
		}
		if static && (hasHeader || hasFooter) {
			pdf, err = c.overlayBands(tab, pdf, req, info, box, nil, 0, 0)
			if err != nil {
				return err
			}
		}
		out = rendered{pdf: pdf}
		return nil
	})
	return out, err
}

func (c *Converter) renderMany(ctx context.Context, req Request) (rendered, error) {
	if heterogeneous(req) {
		return c.renderManyMixed(ctx, req)
	}
	wantHeader := headerActive(req.Header) || headerActive(req.Footer)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type slot struct {
		pdf   []byte
		marks []pdfcpu.Bookmark
		n     int
		err   error
	}
	slots := make([]slot, len(req.Inputs))
	var wg sync.WaitGroup
	for i, in := range req.Inputs {
		wg.Add(1)
		go func(i int, in Input) {
			defer wg.Done()
			part, err := c.renderPlain(ctx, in, req, plainOpts{reserveHeader: wantHeader})
			if err != nil {
				slots[i].err = err
				if !errors.Is(err, errSkipInput) {
					cancel()
				}
				return
			}
			n, err := pdfPageCount(part.pdf)
			if err != nil {
				slots[i].err = err
				cancel()
				return
			}
			slots[i] = slot{pdf: part.pdf, marks: part.marks, n: n}
		}(i, in)
	}
	wg.Wait()
	var parts [][]byte
	var groups [][]pdfcpu.Bookmark
	var counts []int
	var first error
	for _, s := range slots {
		if s.err != nil {
			if errors.Is(s.err, errSkipInput) {
				continue
			}
			if first == nil || errors.Is(first, context.Canceled) {
				first = s.err
			}
			continue
		}
		if s.pdf == nil {
			continue
		}
		parts = append(parts, s.pdf)
		groups = append(groups, s.marks)
		counts = append(counts, s.n)
	}
	if first != nil {
		return rendered{}, first
	}
	if len(parts) == 0 {
		return rendered{}, ErrNoInput
	}
	merged, err := mergePDFs(parts)
	if err != nil {
		return rendered{}, err
	}
	marks := concatBookmarks(groups, counts)
	box, err := req.Page.box(wantHeader, wantHeader)
	if err != nil {
		return rendered{}, err
	}
	box = applySpacing(box, req.Header, req.Footer)
	var links []tocLink
	if req.TOC != nil {
		items := flattenBookmarks(marks, req.TOC.depth())
		tocBox := box
		if req.TOC.Page != nil {
			hasH := headerActive(req.Header)
			hasF := headerActive(req.Footer)
			tb, err := req.TOC.Page.box(hasH, hasF)
			if err != nil {
				return rendered{}, err
			}
			tocBox = applySpacing(tb, req.Header, req.Footer)
		}
		estimate := estimateTOCPages(len(items), tocBox)
		tocPDF, used, err := c.printStandaloneTOC(ctx, req, tocBox, items, estimate)
		if err != nil {
			return rendered{}, err
		}
		tocN, err := pdfPageCount(tocPDF)
		if err != nil {
			return rendered{}, err
		}
		merged, err = mergePDFs([][]byte{tocPDF, merged})
		if err != nil {
			return rendered{}, err
		}
		marks = shiftBookmarks(marks, tocN)
		marks = append([]pdfcpu.Bookmark{{Title: req.TOC.title(), PageFrom: 1}}, marks...)
		for _, it := range used {
			links = append(links, tocLink{level: it.level, page: it.page + tocN})
		}
		if len(links) > 0 {
			merged, err = addTOCAnnotations(merged, links, tocBox)
			if err != nil {
				return rendered{}, err
			}
		}
	}
	if wantHeader {
		merged, err = c.stampMerged(ctx, merged, req, box, marks)
		if err != nil {
			return rendered{}, err
		}
	}
	merged, err = setBookmarks(merged, filterDepth(marks, req.Outline.depth()))
	if err != nil {
		return rendered{}, err
	}
	return rendered{pdf: merged, marks: marks}, nil
}

func (c *Converter) renderPlain(ctx context.Context, in Input, req Request, opts plainOpts) (rendered, error) {
	hasHeader := opts.reserveHeader && headerActive(req.Header)
	hasFooter := opts.reserveHeader && headerActive(req.Footer)
	box, err := req.Page.box(hasHeader, hasFooter)
	if err != nil {
		return rendered{}, err
	}
	if opts.reserveHeader {
		box = applySpacing(box, req.Header, req.Footer)
	}
	var out rendered
	err = c.withTab(ctx, req.Network.Proxy, func(tab context.Context) error {
		tab, cancel := bound(tab, req.Wait.Timeout, req.Network.Timeout)
		defer cancel()
		plain := req
		plain.Header, plain.Footer, plain.TOC = nil, nil, nil
		info, cleanup, err := c.open(tab, navFor(in, plain))
		if err != nil {
			return err
		}
		defer cleanup()
		scale, err := c.scaleFor(tab, req, box)
		if err != nil {
			return err
		}
		pdf, err := c.printPDF(tab, box, scale, plain, info, opts.nativeHeader)
		if err != nil {
			return err
		}
		marks, err := pdfBookmarks(pdf)
		if err != nil {
			return err
		}
		out = rendered{pdf: pdf, marks: marks}
		return nil
	})
	return out, err
}

func (c *Converter) printStandaloneTOC(ctx context.Context, req Request, box pageBox, items []tocItem, estimate int) ([]byte, []tocItem, error) {
	var pdf []byte
	var err error
	used := estimate
	for attempt := 0; attempt < 2; attempt++ {
		frag, ferr := tocFragment(req.TOC, items, used, req.PageOffset)
		if ferr != nil {
			return nil, nil, ferr
		}
		doc := standaloneHTML(frag)
		err = c.withTab(ctx, req.Network.Proxy, func(tab context.Context) error {
			info, cleanup, err := c.open(tab, navSpec{
				in:      FromHTML(doc),
				network: req.Network,
				media:   MediaPrint,
				load:    LoadErrorAbort,
			})
			if err != nil {
				return err
			}
			defer cleanup()
			pdf, err = c.printPDF(tab, box, 1, Request{Page: req.Page, Outline: Outline{Enabled: boolPtr(false)}, Tagged: req.Tagged}, info, false)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		n, err := pdfPageCount(pdf)
		if err != nil {
			return nil, nil, err
		}
		if n == used {
			break
		}
		used = n
	}
	return pdf, items, nil
}

func (c *Converter) stampMerged(ctx context.Context, pdf []byte, req Request, box pageBox, marks []pdfcpu.Bookmark) ([]byte, error) {
	var out []byte
	err := c.withTab(ctx, req.Network.Proxy, func(tab context.Context) error {
		info := pageInfo{Title: req.Metadata.Title, Location: ""}
		if info.Title == "" && len(req.Inputs) == 1 {
			info.Location = displayOf(req.Inputs[0])
		}
		var err error
		out, err = c.overlayBands(tab, pdf, req, info, box, marks, 0, 0)
		return err
	})
	_ = marks
	return out, err
}

func displayOf(in Input) string {
	switch {
	case in.URL != "":
		return in.URL
	case in.File != "":
		return in.File
	case in.BaseDir != "":
		return in.BaseDir
	default:
		return ""
	}
}

func (c *Converter) scaleFor(ctx context.Context, req Request, box pageBox) (float64, error) {
	if !req.Page.SmartShrinking {
		return clampScale(req.Page.Scale), nil
	}
	width, err := documentScrollWidth(ctx)
	if err != nil {
		return 0, fmt.Errorf("printkit: measure layout: %w", err)
	}
	return fitScale(req.Page.Scale, width, box.contentWidthPx(), true), nil
}

func pdfPrintOptions(box pageBox, scale float64, req Request, info pageInfo, nativeHeader bool) ([]chromedp.PDFOption, error) {
	opts := []chromedp.PDFOption{
		chromedp.PDFPaper(chromedp.PaperSize{Width: box.width, Height: box.height}),
		chromedp.PDFMargins(box.marginTop, box.marginRight, box.marginBottom, box.marginLeft),
		chromedp.PDFScale(clampScale(scale)),
	}
	if boolOr(req.Page.PrintBackground, true) {
		opts = append(opts, chromedp.PDFPrintBackground())
	}
	if req.Page.PreferCSSPageSize {
		opts = append(opts, chromedp.PDFPreferCSSPageSize())
	}
	// The tagged structure tree is most of the time and the bytes of a large
	// table, so the default turns it off. Chrome 156 will embed /Outlines
	// without that tree, but every destination then points at the first page.
	// Tagged keeps Chrome's tree and its outline. Untagged builds the outline
	// afterwards from CSS fragmentation and the printed content box.
	if req.Tagged && req.Outline.enabled() {
		on := true
		opts = append(opts, func(p *cdppage.PrintToPDFParams) { p.GenerateDocumentOutline = &on })
	} else if !req.Tagged {
		off := false
		opts = append(opts, func(p *cdppage.PrintToPDFParams) { p.GenerateTaggedPDF = &off })
	}
	if nativeHeader {
		data := HeaderData{
			Title:      info.Title,
			URL:        info.Location,
			PageOffset: req.PageOffset,
		}
		header, err := renderSide(req.Header, data, false)
		if err != nil {
			return nil, err
		}
		footer, err := renderSide(req.Footer, data, true)
		if err != nil {
			return nil, err
		}
		if header != "" {
			opts = append(opts, chromedp.PDFHeaderTemplate(header))
		}
		if footer != "" {
			opts = append(opts, chromedp.PDFFooterTemplate(footer))
		}
		if header != "" && footer == "" {
			opts = append(opts, chromedp.PDFFooterTemplate(`<span></span>`))
		}
		if footer != "" && header == "" {
			opts = append(opts, chromedp.PDFHeaderTemplate(`<span></span>`))
		}
	}
	return opts, nil
}

func (c *Converter) printPDF(ctx context.Context, box pageBox, scale float64, req Request, info pageInfo, nativeHeader bool) ([]byte, error) {
	opts, err := pdfPrintOptions(box, scale, req, info, nativeHeader)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := c.writePrint(ctx, &buf, opts); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if !req.Tagged && bytes.Contains(out, []byte("/StructTreeRoot")) {
		stop := traceDone(c.cfg.trace, "strip")
		out, err = stripStructureTree(out)
		stop()
		if err != nil {
			return nil, err
		}
	}
	if req.Outline.enabled() && !req.Tagged {
		stop := traceDone(c.cfg.trace, "outline")
		marks, err := domBookmarks(ctx, box, scale, req.Outline.depth())
		if err != nil {
			stop()
			return nil, err
		}
		if len(marks) > 0 {
			out, err = setBookmarks(out, marks)
			if err != nil {
				stop()
				return nil, err
			}
		}
		stop()
	}
	return out, nil
}

// canDirectWrite reports whether the PDF can be streamed to the caller with
// no pdfcpu pass and no second copy of the document.
func canDirectWrite(req Request) bool {
	// The outline is written from heading positions after the print, so the
	// bytes have to come back before they can be streamed to the caller.
	if req.Outline.enabled() {
		return false
	}
	if requestNeedsPost(req) {
		return false
	}
	if len(req.Inputs) != 1 || req.Cover != nil || req.TOC != nil {
		return false
	}
	if headerActive(req.Header) || headerActive(req.Footer) {
		return false
	}
	in := req.Inputs[0]
	if headerActive(in.Header) || headerActive(in.Footer) {
		return false
	}
	return true
}

// requestNeedsPost is true when merge, metadata, page ranges, copies, or an
// outline adjustment still has to touch the PDF bytes after printing.
func requestNeedsPost(req Request) bool {
	if req.Cover != nil || len(req.Inputs) != 1 || req.TOC != nil {
		return true
	}
	if strings.TrimSpace(req.PageRanges) != "" || req.Copies > 1 {
		return true
	}
	if req.Metadata != (Metadata{}) {
		return true
	}
	if !req.Outline.enabled() {
		// An untagged print has no Chrome outline to clear. A tagged print
		// may, and clearing it is a pdfcpu pass.
		return req.Tagged
	}
	// Chrome's outline is the full heading tree. A depth limit is applied
	// afterwards. The DOM fallback already honours depth, and filtering
	// again is the same tree.
	if req.Outline.Depth > 0 {
		return true
	}
	return false
}

func (c *Converter) writePDFDirect(ctx context.Context, w io.Writer, req Request) error {
	if err := validateRequest(req); err != nil {
		return err
	}
	in := req.Inputs[0]
	box, err := req.Page.box(false, false)
	if err != nil {
		return err
	}
	return c.withTab(ctx, req.Network.Proxy, func(tab context.Context) error {
		tab, cancel := bound(tab, req.Wait.Timeout, req.Network.Timeout)
		defer cancel()
		info, cleanup, err := c.open(tab, navFor(in, req))
		if err != nil {
			return err
		}
		defer cleanup()
		scale, err := c.scaleFor(tab, req, box)
		if err != nil {
			return err
		}
		opts, err := pdfPrintOptions(box, scale, req, info, false)
		if err != nil {
			return err
		}
		return c.writePrint(tab, w, opts)
	})
}

func renderSide(h *HeaderFooter, data HeaderData, lineAtTop bool) (string, error) {
	if !headerActive(h) {
		return "", nil
	}
	return h.render(data, lineAtTop)
}

func (c *Converter) overlayBands(ctx context.Context, pdf []byte, req Request, info pageInfo, box pageBox, marks []pdfcpu.Bookmark, pageStart, pageTotal int) ([]byte, error) {
	n, err := pdfPageCount(pdf)
	if err != nil {
		return nil, err
	}
	if pageStart <= 0 {
		pageStart = 1
	}
	if pageTotal <= 0 {
		pageTotal = n
	}
	if marks == nil {
		marks, err = pdfBookmarks(pdf)
		if err != nil {
			return nil, err
		}
	}
	labels := sectionLabels(marks, n)
	if headerActive(req.Header) {
		band, err := c.renderBand(ctx, req.Header, labels, info, box, n, pageStart, pageTotal, req.PageOffset, req.Tagged, true)
		if err != nil {
			return nil, err
		}
		pdf, err = stampBand(pdf, band, true)
		if err != nil {
			return nil, err
		}
	}
	if headerActive(req.Footer) {
		band, err := c.renderBand(ctx, req.Footer, labels, info, box, n, pageStart, pageTotal, req.PageOffset, req.Tagged, false)
		if err != nil {
			return nil, err
		}
		pdf, err = stampBand(pdf, band, false)
		if err != nil {
			return nil, err
		}
	}
	// Stamping can drop the outline. Put the tree captured above back.
	if len(marks) > 0 {
		pdf, err = setBookmarks(pdf, marks)
		if err != nil {
			return nil, err
		}
	}
	return pdf, nil
}

func (c *Converter) renderBand(ctx context.Context, hf *HeaderFooter, labels []pageLabel, info pageInfo, box pageBox, n, pageStart, pageTotal, offset int, tagged, top bool) ([]byte, error) {
	height := box.marginTop
	if !top {
		height = box.marginBottom
	}
	if height < MM(8).Inches() {
		height = MM(8).Inches()
	}
	var pages strings.Builder
	for i := 1; i <= n; i++ {
		label := pageLabel{}
		if i-1 < len(labels) {
			label = labels[i-1]
		}
		data := HeaderData{
			Page:       pageStart + i - 1,
			PageCount:  pageTotal,
			PageOffset: offset,
			Section:    label.section,
			Subsection: label.subsection,
			Title:      info.Title,
			URL:        info.Location,
			Static:     true,
		}
		body, err := hf.render(data, !top)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&pages, `<div class="band">%s</div>`, body)
	}
	doc := fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><style>
		@page { size: %fin %fin; margin: 0; }
		html, body { margin: 0; padding: 0; }
		.band { width: %fin; height: %fin; box-sizing: border-box; overflow: hidden; display: flex; align-items: center; page-break-after: always; padding: 0 0.15in; }
		.band:last-child { page-break-after: auto; }
	</style></head><body>%s</body></html>`, box.width, height, box.width, height, pages.String())
	mounted := c.assets.mountHTML(doc)
	defer mounted.cleanup()
	if err := chromedp.Do(ctx, chromedp.Navigate(mounted.url)); err != nil {
		return nil, fmt.Errorf("printkit: header: %w", err)
	}
	opts := []chromedp.PDFOption{
		chromedp.PDFPaper(chromedp.PaperSize{Width: box.width, Height: height}),
		chromedp.PDFMargins(0, 0, 0, 0),
		chromedp.PDFPreferCSSPageSize(),
		chromedp.PDFScale(1),
	}
	if !tagged {
		off := false
		opts = append(opts, func(p *cdppage.PrintToPDFParams) { p.GenerateTaggedPDF = &off })
	}
	buf, err := chromedp.Run(ctx, chromedp.PrintToPDF(opts...))
	if err != nil {
		return nil, fmt.Errorf("printkit: header: %w", err)
	}
	return buf, nil
}

func navFor(in Input, req Request) navSpec {
	media := req.Page.Media
	if media == "" {
		media = MediaPrint
	}
	return navSpec{
		in:            in,
		network:       req.Network,
		wait:          req.Wait,
		inject:        req.Inject,
		media:         media,
		grayscale:     req.Grayscale,
		viewportW:     req.Page.ViewportWidth,
		viewportH:     req.Page.ViewportHeight,
		stripExternal: req.DisableExternalLinks,
		stripInternal: req.DisableInternalLinks,
		load:          req.LoadErrors,
		minFontPx:     req.MinimumFontSize * 96 / 72,
	}
}

func applySpacing(box pageBox, header, footer *HeaderFooter) pageBox {
	if header != nil && !header.Spacing.IsZero() {
		box.marginTop += header.Spacing.Inches()
	}
	if footer != nil && !footer.Spacing.IsZero() {
		box.marginBottom += footer.Spacing.Inches()
	}
	return box
}

func needsStatic(h *HeaderFooter, offset int, continuous bool) (bool, error) {
	if !headerActive(h) {
		return false, nil
	}
	if offset != 0 || continuous {
		return true, nil
	}
	raw, err := h.source(false)
	if err != nil {
		return false, err
	}
	return strings.Contains(raw, "[section]") || strings.Contains(raw, "[subsection]"), nil
}

func applyOutline(pdf []byte, req Request, marks []pdfcpu.Bookmark) ([]byte, error) {
	if !req.Outline.enabled() {
		if !bytes.Contains(pdf, []byte("/Outlines")) {
			return pdf, nil
		}
		return clearBookmarks(pdf)
	}
	if len(marks) == 0 {
		if req.Outline.Depth <= 0 {
			return pdf, nil
		}
		existing, err := pdfBookmarks(pdf)
		if err != nil || len(existing) == 0 {
			return pdf, err
		}
		return setBookmarks(pdf, filterDepth(existing, req.Outline.depth()))
	}
	return setBookmarks(pdf, filterDepth(marks, req.Outline.depth()))
}

func bound(ctx context.Context, limits ...time.Duration) (context.Context, context.CancelFunc) {
	var d time.Duration
	for _, limit := range limits {
		if limit <= 0 {
			continue
		}
		if d == 0 || limit < d {
			d = limit
		}
	}
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func boolPtr(v bool) *bool { return &v }

const ensureHeadingScript = `(() => {
	let i = 0;
	document.querySelectorAll('h1,h2,h3,h4,h5,h6').forEach(h => {
		if (!h.id) h.id = 'printkit-h-' + (i++);
	});
})()`

type tocItem struct {
	level int
	title string
	id    string
	page  int
}

type heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

func tocItemsFrom(ctx context.Context, pass1 []byte, depth int) ([]tocItem, error) {
	bms, err := pdfBookmarks(pass1)
	if err != nil {
		return nil, err
	}
	items := flattenBookmarks(bms, depth)
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[string](headingJSONScript))
	if err != nil {
		return items, nil
	}
	var heads []heading
	if err := json.Unmarshal([]byte(raw), &heads); err != nil {
		return items, nil
	}
	if len(heads) == len(items) {
		for i := range items {
			items[i].id = heads[i].ID
		}
	}
	return items, nil
}

const headingJSONScript = `JSON.stringify([...document.querySelectorAll('h1,h2,h3,h4,h5,h6')].map(h => ({
	level: Number(h.tagName.slice(1)),
	text: (h.innerText || '').trim(),
	id: h.id || ''
})))`

func flattenBookmarks(bms []pdfcpu.Bookmark, depth int) []tocItem {
	var out []tocItem
	var walk func([]pdfcpu.Bookmark, int)
	walk = func(in []pdfcpu.Bookmark, level int) {
		for _, b := range in {
			if depth <= 0 || level <= depth {
				out = append(out, tocItem{level: level, title: b.Title, page: b.PageFrom})
			}
			walk(b.Kids, level+1)
		}
	}
	walk(bms, 1)
	return out
}

func estimateTOCPages(n int, box pageBox) int {
	if n <= 0 {
		return 1
	}
	contentPx := (box.height - box.marginTop - box.marginBottom) * 96
	const row = 26.0
	const header = 56.0
	per := int((contentPx - header) / row)
	if per < 1 {
		per = 1
	}
	pages := (n + per - 1) / per
	if pages < 1 {
		return 1
	}
	return pages
}

// DefaultTOCTemplate is the HTML template used when TOC.Template is empty.
// Execute it with a value that has Title and Entries. Each entry has Level,
// Title, Page, Href, and Indent.
const DefaultTOCTemplate = `<section id="printkit-toc" style="font-family:'Liberation Sans',DejaVu Sans,sans-serif;color:#1c1917;"><div style="font-size:22pt;font-weight:600;margin:0 0 14px;">{{.Title}}</div>{{range .Entries}}<div style="display:flex;justify-content:space-between;gap:12px;margin:0 0 6px;padding-left:{{.Indent}}px;font-size:12pt;line-height:1.4;">{{if .Href}}<a href="{{.Href}}" style="color:#1d4ed8;text-decoration:none;">{{.Title}}</a>{{else}}<span>{{.Title}}</span>{{end}}<span>{{.Page}}</span></div>{{end}}</section>`

var defaultTOCTmpl = template.Must(template.New("toc").Parse(DefaultTOCTemplate))

type tocView struct {
	Title   string
	Entries []tocEntryView
}

type tocEntryView struct {
	Level  int
	Title  string
	Page   int
	Href   string
	Indent int
}

func tocFragment(toc *TOC, items []tocItem, tocPages, offset int) (string, error) {
	view := tocView{Title: toc.title()}
	for _, it := range items {
		shown := it.page + tocPages + offset
		if shown < 1 {
			shown = 1
		}
		href := ""
		if it.id != "" {
			href = "#" + it.id
		}
		view.Entries = append(view.Entries, tocEntryView{
			Level:  it.level,
			Title:  it.title,
			Page:   shown,
			Href:   href,
			Indent: max(it.level-1, 0) * 16,
		})
	}
	tmpl := defaultTOCTmpl
	if toc != nil && strings.TrimSpace(toc.Template) != "" {
		parsed, err := template.New("toc").Parse(toc.Template)
		if err != nil {
			return "", optionf("TOC.Template", "%s", err.Error())
		}
		tmpl = parsed
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, view); err != nil {
		return "", fmt.Errorf("printkit: toc template: %w", err)
	}
	return b.String(), nil
}

func (c *Converter) renderSeparateTOC(ctx context.Context, in Input, req Request) (rendered, error) {
	bodyReq := req
	bodyReq.TOC = nil
	hasHeader := headerActive(bodyReq.Header)
	hasFooter := headerActive(bodyReq.Footer)
	box, err := bodyReq.Page.box(hasHeader, hasFooter)
	if err != nil {
		return rendered{}, err
	}
	box = applySpacing(box, bodyReq.Header, bodyReq.Footer)
	body, err := c.renderPlain(ctx, in, bodyReq, plainOpts{reserveHeader: hasHeader || hasFooter})
	if err != nil {
		return rendered{}, err
	}
	items := flattenBookmarks(body.marks, req.TOC.depth())
	tocBox := box
	if req.TOC.Page != nil {
		tb, err := req.TOC.Page.box(false, false)
		if err != nil {
			return rendered{}, err
		}
		tocBox = tb
	}
	estimate := estimateTOCPages(len(items), tocBox)
	tocPDF, used, err := c.printStandaloneTOC(ctx, req, tocBox, items, estimate)
	if err != nil {
		return rendered{}, err
	}
	tocN, err := pdfPageCount(tocPDF)
	if err != nil {
		return rendered{}, err
	}
	bodyPDF := body.pdf
	bodyN, err := pdfPageCount(bodyPDF)
	if err != nil {
		return rendered{}, err
	}
	if hasHeader || hasFooter {
		info := pageInfo{Title: req.Metadata.Title, Location: displayOf(in)}
		err = c.withTab(ctx, req.Network.Proxy, func(tab context.Context) error {
			var stampErr error
			bodyPDF, stampErr = c.overlayBands(tab, bodyPDF, bodyReq, info, box, body.marks, tocN+1, tocN+bodyN)
			return stampErr
		})
		if err != nil {
			return rendered{}, err
		}
	}
	merged, err := mergePDFs([][]byte{tocPDF, bodyPDF})
	if err != nil {
		return rendered{}, err
	}
	marks := shiftBookmarks(body.marks, tocN)
	marks = append([]pdfcpu.Bookmark{{Title: req.TOC.title(), PageFrom: 1}}, marks...)
	var links []tocLink
	for _, it := range used {
		links = append(links, tocLink{level: it.level, page: it.page + tocN})
	}
	if len(links) > 0 {
		merged, err = addTOCAnnotations(merged, links, tocBox)
		if err != nil {
			return rendered{}, err
		}
	}
	merged, err = setBookmarks(merged, filterDepth(marks, req.Outline.depth()))
	if err != nil {
		return rendered{}, err
	}
	return rendered{pdf: merged, marks: marks}, nil
}

func (c *Converter) renderManyMixed(ctx context.Context, req Request) (rendered, error) {
	type part struct {
		pdf   []byte
		marks []pdfcpu.Bookmark
		n     int
		spec  Request
		box   pageBox
		in    Input
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	slots := make([]part, len(req.Inputs))
	errs := make([]error, len(req.Inputs))
	var wg sync.WaitGroup
	for i, in := range req.Inputs {
		wg.Add(1)
		go func(i int, in Input) {
			defer wg.Done()
			spec := withInput(req, in, false)
			spec.TOC = nil
			hasH := headerActive(spec.Header)
			hasF := headerActive(spec.Footer)
			box, err := spec.Page.box(hasH, hasF)
			if err != nil {
				errs[i] = err
				cancel()
				return
			}
			box = applySpacing(box, spec.Header, spec.Footer)
			plain, err := c.renderPlain(ctx, in, spec, plainOpts{reserveHeader: hasH || hasF})
			if errors.Is(err, errSkipInput) {
				errs[i] = err
				return
			}
			if err != nil {
				errs[i] = err
				cancel()
				return
			}
			n, err := pdfPageCount(plain.pdf)
			if err != nil {
				errs[i] = err
				cancel()
				return
			}
			slots[i] = part{pdf: plain.pdf, marks: plain.marks, n: n, spec: spec, box: box, in: in}
		}(i, in)
	}
	wg.Wait()
	var parts []part
	var first error
	for i, s := range slots {
		if errs[i] != nil {
			if errors.Is(errs[i], errSkipInput) {
				continue
			}
			if first == nil || errors.Is(first, context.Canceled) {
				first = errs[i]
			}
			continue
		}
		if s.pdf == nil {
			continue
		}
		parts = append(parts, s)
	}
	if first != nil {
		return rendered{}, first
	}
	if len(parts) == 0 {
		return rendered{}, ErrNoInput
	}
	total := 0
	for _, p := range parts {
		total += p.n
	}
	var pdfs [][]byte
	var groups [][]pdfcpu.Bookmark
	var counts []int
	base := 0
	for _, p := range parts {
		pdf := p.pdf
		if headerActive(p.spec.Header) || headerActive(p.spec.Footer) {
			info := pageInfo{Title: req.Metadata.Title, Location: displayOf(p.in)}
			err := c.withTab(ctx, req.Network.Proxy, func(tab context.Context) error {
				var stampErr error
				pdf, stampErr = c.overlayBands(tab, pdf, p.spec, info, p.box, p.marks, base+1, total)
				return stampErr
			})
			if err != nil {
				return rendered{}, err
			}
		}
		pdfs = append(pdfs, pdf)
		groups = append(groups, p.marks)
		counts = append(counts, p.n)
		base += p.n
	}
	merged, err := mergePDFs(pdfs)
	if err != nil {
		return rendered{}, err
	}
	marks := concatBookmarks(groups, counts)
	box, err := req.Page.box(headerActive(req.Header), headerActive(req.Footer))
	if err != nil {
		return rendered{}, err
	}
	box = applySpacing(box, req.Header, req.Footer)
	if req.TOC != nil {
		items := flattenBookmarks(marks, req.TOC.depth())
		tocBox := box
		if req.TOC.Page != nil {
			tb, err := req.TOC.Page.box(false, false)
			if err != nil {
				return rendered{}, err
			}
			tocBox = tb
		}
		estimate := estimateTOCPages(len(items), tocBox)
		tocPDF, used, err := c.printStandaloneTOC(ctx, req, tocBox, items, estimate)
		if err != nil {
			return rendered{}, err
		}
		tocN, err := pdfPageCount(tocPDF)
		if err != nil {
			return rendered{}, err
		}
		merged, err = mergePDFs([][]byte{tocPDF, merged})
		if err != nil {
			return rendered{}, err
		}
		marks = shiftBookmarks(marks, tocN)
		marks = append([]pdfcpu.Bookmark{{Title: req.TOC.title(), PageFrom: 1}}, marks...)
		var links []tocLink
		for _, it := range used {
			links = append(links, tocLink{level: it.level, page: it.page + tocN})
		}
		if len(links) > 0 {
			merged, err = addTOCAnnotations(merged, links, tocBox)
			if err != nil {
				return rendered{}, err
			}
		}
	}
	merged, err = setBookmarks(merged, filterDepth(marks, req.Outline.depth()))
	if err != nil {
		return rendered{}, err
	}
	return rendered{pdf: merged, marks: marks}, nil
}

func injectTOC(ctx context.Context, fragment string) error {
	payload, err := json.Marshal(fragment)
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`(() => {
		const html = %s;
		const old = document.getElementById('printkit-toc-wrap');
		if (old) old.remove();
		const wrap = document.createElement('div');
		wrap.id = 'printkit-toc-wrap';
		wrap.style.breakAfter = 'page';
		wrap.innerHTML = html;
		if (!document.body) throw new Error('document has no body');
		document.body.insertBefore(wrap, document.body.firstChild);
	})()`, payload)
	return evalJS(ctx, script)
}

func standaloneHTML(fragment string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><title>Table of Contents</title>
<style>html,body{margin:0;padding:0;} a{color:#1d4ed8;}</style></head><body>` + fragment + `</body></html>`
}
