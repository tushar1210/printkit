package printkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
)

func (c *Converter) renderPDF(ctx context.Context, req Request) ([]byte, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	var pieces [][]byte
	var marks []pdfcpu.Bookmark
	if req.Cover != nil {
		cover, err := c.renderPlain(ctx, *req.Cover, req, plainOpts{})
		if err != nil {
			if errors.Is(err, errSkipInput) {
				return nil, ErrNoInput
			}
			return nil, err
		}
		pieces = append(pieces, cover.pdf)
	}
	switch len(req.Inputs) {
	case 0:
	case 1:
		body, err := c.renderSingle(ctx, req.Inputs[0], req)
		if err != nil {
			if errors.Is(err, errSkipInput) {
				return nil, ErrNoInput
			}
			return nil, err
		}
		pieces = append(pieces, body.pdf)
		marks = body.marks
	default:
		body, err := c.renderMany(ctx, req)
		if err != nil {
			return nil, err
		}
		pieces = append(pieces, body.pdf)
		marks = body.marks
	}
	if len(pieces) == 0 {
		return nil, ErrNoInput
	}
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
	_, err := req.Page.box(headerActive(req.Header), headerActive(req.Footer))
	return err
}

func (c *Converter) renderSingle(ctx context.Context, in Input, req Request) (rendered, error) {
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
			if err := injectTOC(tab, tocFragment(req.TOC.title(), items, estimate, req.PageOffset)); err != nil {
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
				if err := injectTOC(tab, tocFragment(req.TOC.title(), items, shift, req.PageOffset)); err != nil {
					return err
				}
				pdf, err = c.printPDF(tab, box, scale, req, info, !static)
				if err != nil {
					return err
				}
			}
			if static && (hasHeader || hasFooter) {
				pdf, err = c.overlayBands(tab, pdf, req, info, box, nil)
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
			pdf, err = c.overlayBands(tab, pdf, req, info, box, nil)
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
	wantHeader := headerActive(req.Header) || headerActive(req.Footer)
	var parts [][]byte
	var groups [][]pdfcpu.Bookmark
	var counts []int
	for _, in := range req.Inputs {
		part, err := c.renderPlain(ctx, in, req, plainOpts{reserveHeader: wantHeader})
		if errors.Is(err, errSkipInput) {
			continue
		}
		if err != nil {
			return rendered{}, err
		}
		n, err := pdfPageCount(part.pdf)
		if err != nil {
			return rendered{}, err
		}
		parts = append(parts, part.pdf)
		groups = append(groups, part.marks)
		counts = append(counts, n)
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
		estimate := estimateTOCPages(len(items), box)
		tocPDF, used, err := c.printStandaloneTOC(ctx, req, box, items, estimate)
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
			merged, err = addTOCAnnotations(merged, links, box)
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
		doc := standaloneHTML(tocFragment(req.TOC.title(), items, used, req.PageOffset))
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
			pdf, err = c.printPDF(tab, box, 1, Request{Page: req.Page, Outline: Outline{Enabled: boolPtr(false)}}, info, false)
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
		out, err = c.overlayBands(tab, pdf, req, info, box, marks)
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

func (c *Converter) printPDF(ctx context.Context, box pageBox, scale float64, req Request, info pageInfo, nativeHeader bool) ([]byte, error) {
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
	if req.Outline.enabled() {
		on := true
		opts = append(opts, func(p *cdppage.PrintToPDFParams) { p.GenerateDocumentOutline = &on })
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
	buf, err := chromedp.Run(ctx, chromedp.PrintToPDF(opts...))
	if err != nil {
		return nil, fmt.Errorf("printkit: print pdf: %w", err)
	}
	if len(buf) < 5 || string(buf[:5]) != "%PDF-" {
		return nil, fmt.Errorf("printkit: chromium returned an empty pdf")
	}
	return buf, nil
}

func renderSide(h *HeaderFooter, data HeaderData, lineAtTop bool) (string, error) {
	if !headerActive(h) {
		return "", nil
	}
	return h.render(data, lineAtTop)
}

func (c *Converter) overlayBands(ctx context.Context, pdf []byte, req Request, info pageInfo, box pageBox, marks []pdfcpu.Bookmark) ([]byte, error) {
	n, err := pdfPageCount(pdf)
	if err != nil {
		return nil, err
	}
	if marks == nil {
		marks, err = pdfBookmarks(pdf)
		if err != nil {
			return nil, err
		}
	}
	labels := sectionLabels(marks, n)
	if headerActive(req.Header) {
		band, err := c.renderBand(ctx, req.Header, labels, info, box, n, req.PageOffset, true)
		if err != nil {
			return nil, err
		}
		pdf, err = stampBand(pdf, band, true)
		if err != nil {
			return nil, err
		}
	}
	if headerActive(req.Footer) {
		band, err := c.renderBand(ctx, req.Footer, labels, info, box, n, req.PageOffset, false)
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

func (c *Converter) renderBand(ctx context.Context, hf *HeaderFooter, labels []pageLabel, info pageInfo, box pageBox, n, offset int, top bool) ([]byte, error) {
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
			Page:       i,
			PageCount:  n,
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
	buf, err := chromedp.Run(ctx, chromedp.PrintToPDF(
		chromedp.PDFPaper(chromedp.PaperSize{Width: box.width, Height: height}),
		chromedp.PDFMargins(0, 0, 0, 0),
		chromedp.PDFPreferCSSPageSize(),
		chromedp.PDFScale(1),
	))
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

func tocFragment(title string, items []tocItem, tocPages, offset int) string {
	var b strings.Builder
	b.WriteString(`<section id="printkit-toc" style="font-family:'Liberation Sans',DejaVu Sans,sans-serif;color:#1c1917;">`)
	fmt.Fprintf(&b, `<div style="font-size:22pt;font-weight:600;margin:0 0 14px;">%s</div>`, htmlEscape(title))
	for _, it := range items {
		shown := it.page + tocPages + offset
		if shown < 1 {
			shown = 1
		}
		indent := max(it.level-1, 0) * 16
		href := ""
		if it.id != "" {
			href = ` href="#` + htmlEscape(it.id) + `"`
		}
		fmt.Fprintf(&b, `<div style="display:flex;justify-content:space-between;gap:12px;margin:0 0 6px;padding-left:%dpx;font-size:12pt;line-height:1.4;"><a%s style="color:#1d4ed8;text-decoration:none;">%s</a><span>%d</span></div>`,
			indent, href, htmlEscape(it.title), shown)
	}
	b.WriteString(`</section>`)
	return b.String()
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

func htmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
	).Replace(s)
}
