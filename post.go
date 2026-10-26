package printkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func init() {
	// Keep pdfcpu from creating a config directory when a call passes a nil
	// configuration. DisableConfigDir is deprecated and sets this same switch.
	model.ConfigPath = "disable"
}

func mergePDFs(parts [][]byte) ([]byte, error) {
	if len(parts) == 0 {
		return nil, ErrNoInput
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	rs := make([]io.ReadSeeker, len(parts))
	for i, p := range parts {
		rs[i] = bytes.NewReader(p)
	}
	var buf bytes.Buffer
	if err := api.MergeRaw(context.Background(), rs, &buf, false, nil); err != nil {
		return nil, fmt.Errorf("printkit: merge pdf: %w", err)
	}
	return buf.Bytes(), nil
}

func pdfPageCount(b []byte) (int, error) {
	n, err := api.PageCount(context.Background(), bytes.NewReader(b), nil)
	if err != nil {
		return 0, fmt.Errorf("printkit: page count: %w", err)
	}
	return n, nil
}

func pdfBookmarks(b []byte) ([]pdfcpu.Bookmark, error) {
	bms, err := api.Bookmarks(context.Background(), bytes.NewReader(b), nil)
	if err != nil {
		if errors.Is(err, pdfcpu.ErrNoBookmarks) || strings.Contains(strings.ToLower(err.Error()), "no bookmark") {
			return nil, nil
		}
		return nil, fmt.Errorf("printkit: read outline: %w", err)
	}
	return bms, nil
}

func setBookmarks(b []byte, bms []pdfcpu.Bookmark) ([]byte, error) {
	if len(bms) == 0 {
		return b, nil
	}
	var buf bytes.Buffer
	err := api.AddBookmarks(context.Background(), bytes.NewReader(b), &buf, bms, true, nil)
	if err != nil {
		buf.Reset()
		err = api.AddBookmarks(context.Background(), bytes.NewReader(b), &buf, bms, false, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("printkit: write outline: %w", err)
	}
	return buf.Bytes(), nil
}

func clearBookmarks(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	err := api.RemoveBookmarks(context.Background(), bytes.NewReader(b), &buf, nil)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no bookmark") {
			return b, nil
		}
		return nil, fmt.Errorf("printkit: remove outline: %w", err)
	}
	return buf.Bytes(), nil
}

func setMetadata(b []byte, meta Metadata) ([]byte, error) {
	if meta == (Metadata{}) {
		return b, nil
	}
	ctx, err := api.ReadContext(context.Background(), bytes.NewReader(b), nil)
	if err != nil {
		return nil, fmt.Errorf("printkit: metadata: %w", err)
	}
	dict, err := infoDict(ctx)
	if err != nil {
		return nil, err
	}
	put := func(key, val string) error {
		if val == "" {
			return nil
		}
		escaped, err := types.EscapedUTF16String(val)
		if err != nil {
			return fmt.Errorf("printkit: metadata %s: %w", key, err)
		}
		dict.Update(key, types.StringLiteral(*escaped))
		return nil
	}
	for _, field := range []struct{ key, val string }{
		{"Title", meta.Title},
		{"Author", meta.Author},
		{"Subject", meta.Subject},
		{"Keywords", meta.Keywords},
	} {
		if err := put(field.key, field.val); err != nil {
			return nil, err
		}
	}
	creator := meta.Creator
	if creator == "" {
		creator = "printkit"
	}
	producer := meta.Producer
	if producer == "" {
		producer = "printkit " + Version
	}
	if err := put("Creator", creator); err != nil {
		return nil, err
	}
	if err := put("Producer", producer); err != nil {
		return nil, err
	}
	// pdfcpu rewrites Producer on write unless the info dictionary is preserved.
	ctx.PreserveInfoDict = true
	var buf bytes.Buffer
	if err := api.WriteContext(context.Background(), ctx, &buf); err != nil {
		return nil, fmt.Errorf("printkit: metadata: %w", err)
	}
	return buf.Bytes(), nil
}

func infoDict(ctx *model.Context) (types.Dict, error) {
	if ctx.Info != nil {
		dict, err := ctx.DereferenceDict(*ctx.Info)
		if err != nil {
			return nil, fmt.Errorf("printkit: metadata: %w", err)
		}
		if dict != nil {
			return dict, nil
		}
	}
	dict := types.NewDict()
	ref, err := ctx.IndRefForNewObject(dict)
	if err != nil {
		return nil, fmt.Errorf("printkit: metadata: %w", err)
	}
	ctx.Info = ref
	return dict, nil
}

func trimPDF(b []byte, ranges string) ([]byte, error) {
	ranges = strings.ReplaceAll(strings.TrimSpace(ranges), " ", "")
	if ranges == "" {
		return b, nil
	}
	// pdfcpu wants one token per entry ("1", "3", "5-7"), not a single "1,3".
	parts, err := api.ParsePageSelection(ranges)
	if err != nil {
		return nil, optionf("PageRanges", "%s", err.Error())
	}
	var buf bytes.Buffer
	if err := api.Trim(context.Background(), bytes.NewReader(b), &buf, parts, nil); err != nil {
		return nil, fmt.Errorf("printkit: page ranges: %w", err)
	}
	if buf.Len() == 0 {
		return nil, optionf("PageRanges", "no pages selected")
	}
	return buf.Bytes(), nil
}

func parsePageRanges(spec string, n int) ([]int, error) {
	spec = strings.ReplaceAll(spec, " ", "")
	if spec == "" {
		out := make([]int, n)
		for i := 1; i <= n; i++ {
			out[i-1] = i
		}
		return out, nil
	}
	var out []int
	seen := map[int]bool{}
	for _, part := range strings.Split(spec, ",") {
		if part == "" {
			continue
		}
		start, end, err := parseRangePart(part, n)
		if err != nil {
			return nil, err
		}
		for p := start; p <= end; p++ {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		return nil, optionf("PageRanges", "no pages selected")
	}
	return out, nil
}

func parseRangePart(part string, n int) (int, int, error) {
	if strings.Contains(part, "-") {
		a, b, _ := strings.Cut(part, "-")
		start, end := 1, n
		var err error
		if a != "" {
			start, err = strconv.Atoi(a)
			if err != nil {
				return 0, 0, optionf("PageRanges", "invalid range %q", part)
			}
		}
		if b != "" {
			end, err = strconv.Atoi(b)
			if err != nil {
				return 0, 0, optionf("PageRanges", "invalid range %q", part)
			}
		}
		if start < 1 || end < start {
			return 0, 0, optionf("PageRanges", "invalid range %q", part)
		}
		if start > n {
			return 0, 0, optionf("PageRanges", "range %q starts past the last page", part)
		}
		if end > n {
			end = n
		}
		return start, end, nil
	}
	p, err := strconv.Atoi(part)
	if err != nil || p < 1 || p > n {
		return 0, 0, optionf("PageRanges", "invalid page %q", part)
	}
	return p, p, nil
}

func shiftBookmarks(bms []pdfcpu.Bookmark, by int) []pdfcpu.Bookmark {
	if by == 0 {
		return bms
	}
	out := make([]pdfcpu.Bookmark, len(bms))
	for i, b := range bms {
		b.PageFrom += by
		b.Kids = shiftBookmarks(b.Kids, by)
		b.Parent = nil
		out[i] = b
	}
	return out
}

func concatBookmarks(groups [][]pdfcpu.Bookmark, counts []int) []pdfcpu.Bookmark {
	var out []pdfcpu.Bookmark
	off := 0
	for i, g := range groups {
		out = append(out, shiftBookmarks(g, off)...)
		if i < len(counts) {
			off += counts[i]
		}
	}
	return out
}

func filterDepth(bms []pdfcpu.Bookmark, depth int) []pdfcpu.Bookmark {
	if depth <= 0 {
		return bms
	}
	var walk func([]pdfcpu.Bookmark, int) []pdfcpu.Bookmark
	walk = func(in []pdfcpu.Bookmark, level int) []pdfcpu.Bookmark {
		if level > depth {
			return nil
		}
		out := make([]pdfcpu.Bookmark, 0, len(in))
		for _, b := range in {
			b.Kids = walk(b.Kids, level+1)
			b.Parent = nil
			out = append(out, b)
		}
		return out
	}
	return walk(bms, 1)
}

func remapBookmarks(bms []pdfcpu.Bookmark, oldToNew map[int]int) []pdfcpu.Bookmark {
	var out []pdfcpu.Bookmark
	for _, b := range bms {
		kids := remapBookmarks(b.Kids, oldToNew)
		b.Parent = nil
		if np, ok := oldToNew[b.PageFrom]; ok {
			b.PageFrom = np
			b.Kids = kids
			out = append(out, b)
			continue
		}
		if len(kids) == 0 {
			continue
		}
		b.PageFrom = kids[0].PageFrom
		b.Kids = kids
		out = append(out, b)
	}
	return out
}

type pageLabel struct {
	section    string
	subsection string
}

func sectionLabels(bms []pdfcpu.Bookmark, n int) []pageLabel {
	type item struct {
		level int
		page  int
		title string
	}
	var items []item
	var walk func([]pdfcpu.Bookmark, int)
	walk = func(in []pdfcpu.Bookmark, level int) {
		for _, b := range in {
			items = append(items, item{level, b.PageFrom, b.Title})
			walk(b.Kids, level+1)
		}
	}
	walk(bms, 1)
	labels := make([]pageLabel, n)
	for p := 1; p <= n; p++ {
		var sec, sub string
		for _, it := range items {
			if it.page <= 0 || it.page > p {
				continue
			}
			switch it.level {
			case 1:
				sec = it.title
				sub = ""
			case 2:
				sub = it.title
			}
		}
		labels[p-1] = pageLabel{section: sec, subsection: sub}
	}
	return labels
}

func stampBand(dst, src []byte, top bool) ([]byte, error) {
	n, err := pdfPageCount(dst)
	if err != nil {
		return nil, err
	}
	sn, err := pdfPageCount(src)
	if err != nil {
		return nil, err
	}
	if sn == 0 {
		return dst, nil
	}
	pos := "tc"
	if !top {
		pos = "bc"
	}
	desc := "scale:1 abs, pos:" + pos + ", off:0 0, rot:0, op:1"
	m := make(map[int]*model.Watermark, n)
	for i := 1; i <= n; i++ {
		srcPage := i
		if srcPage > sn {
			srcPage = sn
		}
		wm, err := api.PDFWatermarkForReadSeeker(context.Background(), bytes.NewReader(src), srcPage, desc, true, false, types.POINTS)
		if err != nil {
			return nil, fmt.Errorf("printkit: header stamp: %w", err)
		}
		m[i] = wm
	}
	var buf bytes.Buffer
	if err := api.AddWatermarksMap(context.Background(), bytes.NewReader(dst), &buf, m, nil); err != nil {
		return nil, fmt.Errorf("printkit: header stamp: %w", err)
	}
	return buf.Bytes(), nil
}

type tocLink struct {
	level int
	page  int // 1-based page in the final document
}

func addTOCAnnotations(pdf []byte, links []tocLink, box pageBox) ([]byte, error) {
	if len(links) == 0 {
		return pdf, nil
	}
	contentPt := (box.height - box.marginTop - box.marginBottom) * 72
	const row = 18.0
	const header = 40.0
	per := int((contentPt - header) / row)
	if per < 1 {
		per = 1
	}
	anns := map[int][]model.AnnotationRenderer{}
	for i, link := range links {
		tocPage := i/per + 1
		rowIndex := i % per
		top := box.height*72 - box.marginTop*72 - header - float64(rowIndex)*row
		bottom := top - row + 2
		left := box.marginLeft*72 + float64(max(link.level-1, 0))*14
		right := box.width*72 - box.marginRight*72
		if link.page < 1 {
			continue
		}
		dest := &model.Destination{Typ: model.DestFit, PageNr: link.page}
		rect := types.NewRectangle(left, bottom, right, top)
		ann := model.NewLinkAnnotation(*rect, 0, "", "", "", 0, nil, dest, "", nil, false, 0, model.BSSolid)
		anns[tocPage] = append(anns[tocPage], ann)
	}
	if len(anns) == 0 {
		return pdf, nil
	}
	var buf bytes.Buffer
	if err := api.AddAnnotationsMap(context.Background(), bytes.NewReader(pdf), &buf, anns, nil); err != nil {
		return nil, fmt.Errorf("printkit: toc links: %w", err)
	}
	return buf.Bytes(), nil
}

// stripStructureTree drops Chromium's tagged PDF structure while keeping the
// document outline. Chrome 156 emits /Outlines only when it also writes the
// structure tree. Each outline item points at that tree with /SE, so a normal
// mark-and-sweep still keeps every structure element. The fast path copies the
// objects reachable from the catalog and info dictionary and does not follow
// /SE, /StructTreeRoot, or /MarkInfo. A PDF it cannot rewrite falls back to
// pdfcpu, which parses the whole file.
func stripStructureTree(b []byte) ([]byte, error) {
	if !bytes.Contains(b, []byte("/StructTreeRoot")) {
		return b, nil
	}
	if out, err := stripStructureFast(b); err == nil {
		return out, nil
	}
	return stripStructureSlow(b)
}

func stripStructureSlow(b []byte) ([]byte, error) {
	// Chrome 156 stores the document outline inside the tagged structure tree.
	// Copy the bookmarks first, then drop both so the tree's objects can be freed.
	marks, err := pdfBookmarks(b)
	if err != nil {
		return nil, err
	}
	ctx, err := api.ReadContext(context.Background(), bytes.NewReader(b), nil)
	if err != nil {
		return nil, fmt.Errorf("printkit: strip structure: %w", err)
	}
	root, err := ctx.Catalog()
	if err != nil {
		return nil, fmt.Errorf("printkit: strip structure: %w", err)
	}
	// pdfcpu's optimizer does not drop unreferenced objects, and Chrome's
	// outline dictionary keeps the structure tree reachable. Unlink both,
	// free what the catalog and info dictionary cannot reach, then write the
	// bookmarks back as a plain outline.
	root.Delete("StructTreeRoot")
	root.Delete("MarkInfo")
	root.Delete("Outlines")
	if err := dropUnreachable(ctx); err != nil {
		return nil, fmt.Errorf("printkit: strip structure: %w", err)
	}
	var buf bytes.Buffer
	if err := api.WriteContext(context.Background(), ctx, &buf); err != nil {
		return nil, fmt.Errorf("printkit: strip structure: %w", err)
	}
	out := buf.Bytes()
	if len(out) < 5 || string(out[:5]) != "%PDF-" {
		return nil, fmt.Errorf("printkit: strip structure: empty pdf")
	}
	if len(marks) == 0 {
		return out, nil
	}
	return setBookmarks(out, marks)
}

func dropUnreachable(ctx *model.Context) error {
	seen := map[int]bool{0: true}
	var walk func(types.Object) error
	walk = func(o types.Object) error {
		if o == nil {
			return nil
		}
		switch obj := o.(type) {
		case types.IndirectRef:
			nr := int(obj.ObjectNumber)
			if seen[nr] {
				return nil
			}
			seen[nr] = true
			next, err := ctx.Dereference(obj)
			if err != nil || next == nil {
				return err
			}
			return walk(next)
		case types.Dict:
			for _, v := range obj {
				if err := walk(v); err != nil {
					return err
				}
			}
		case types.StreamDict:
			return walk(obj.Dict)
		case types.Array:
			for _, v := range obj {
				if err := walk(v); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, seed := range []*types.IndirectRef{ctx.Root, ctx.Info, ctx.Encrypt} {
		if seed == nil {
			continue
		}
		if err := walk(*seed); err != nil {
			return err
		}
	}
	for nr, entry := range ctx.Table {
		if entry == nil || entry.Free || seen[nr] {
			continue
		}
		if err := ctx.FreeObject(nr); err != nil {
			return err
		}
	}
	return nil
}

func repeatPDF(b []byte, copies int, collate bool) ([]byte, error) {
	if copies <= 1 {
		return b, nil
	}
	marks, err := pdfBookmarks(b)
	if err != nil {
		return nil, err
	}
	n, err := pdfPageCount(b)
	if err != nil {
		return nil, err
	}
	if collate {
		parts := make([][]byte, copies)
		for i := range parts {
			parts[i] = b
		}
		merged, err := mergePDFs(parts)
		if err != nil {
			return nil, err
		}
		if len(marks) == 0 {
			return merged, nil
		}
		groups := make([][]pdfcpu.Bookmark, copies)
		counts := make([]int, copies)
		for i := 0; i < copies; i++ {
			groups[i] = marks
			counts[i] = n
		}
		return setBookmarks(merged, concatBookmarks(groups, counts))
	}
	sel := make([]string, 0, n*copies)
	for p := 1; p <= n; p++ {
		page := strconv.Itoa(p)
		for c := 0; c < copies; c++ {
			sel = append(sel, page)
		}
	}
	var buf bytes.Buffer
	if err := api.Collect(context.Background(), bytes.NewReader(b), &buf, sel, nil); err != nil {
		return nil, fmt.Errorf("printkit: copies: %w", err)
	}
	out := buf.Bytes()
	if len(marks) == 0 {
		return out, nil
	}
	return setBookmarks(out, scaleBookmarkPages(marks, copies))
}

func scaleBookmarkPages(bms []pdfcpu.Bookmark, copies int) []pdfcpu.Bookmark {
	out := make([]pdfcpu.Bookmark, len(bms))
	for i, b := range bms {
		if b.PageFrom > 0 {
			b.PageFrom = (b.PageFrom-1)*copies + 1
		}
		b.Kids = scaleBookmarkPages(b.Kids, copies)
		b.Parent = nil
		out[i] = b
	}
	return out
}
