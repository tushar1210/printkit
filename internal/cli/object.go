package cli

import (
	"os"
	"strconv"
	"strings"

	"github.com/tushar1210/printkit"
)

type argEvent struct {
	flag string
	vals []string
	word string
}

func scanArgs(mode string, args []string) ([]argEvent, error) {
	var events []argEvent
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			for _, word := range args[i+1:] {
				events = append(events, argEvent{word: word})
			}
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			events = append(events, argEvent{word: a})
			continue
		}
		name, inline, hasInline := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if name == "" || strings.HasPrefix(a, "---") {
			return nil, usagef("unknown flag %q", a)
		}
		if reason, bad := unsupported[name]; bad {
			return nil, usagef("unsupported flag --%s: %s", name, reason)
		}
		spec, ok := flagSpecs[name]
		if !ok {
			return nil, usagef("unknown flag --%s", name)
		}
		if !strings.Contains(spec.modes, mode[:1]) {
			return nil, usagef("--%s is not valid for %s", name, mode)
		}
		if spec.arity == 0 && hasInline {
			return nil, usagef("--%s does not take a value", name)
		}
		vals := []string{}
		if hasInline {
			vals = append(vals, inline)
		}
		need := spec.arity - len(vals)
		if need > 0 {
			if i+need >= len(args) {
				return nil, usagef("--%s needs %d value(s)", name, spec.arity)
			}
			vals = append(vals, args[i+1:i+1+need]...)
			i += need
		}
		events = append(events, argEvent{flag: name, vals: vals})
	}
	return events, nil
}

// pageScoped flags apply to the object they follow. Flags before the first
// object, and flags that are not in this set, apply to the whole conversion.
var pageScoped = map[string]bool{
	"page-size": true, "page-width": true, "page-height": true, "orientation": true,
	"margin-top": true, "margin-right": true, "margin-bottom": true, "margin-left": true,
	"zoom": true, "dpi": true,
	"print-media-type": true, "no-print-media-type": true,
	"background": true, "no-background": true,
	"disable-smart-shrinking": true, "enable-smart-shrinking": true,
	"prefer-css-page-size": true, "viewport-size": true,
	"header-html": true, "footer-html": true,
	"header-left": true, "header-center": true, "header-right": true,
	"footer-left": true, "footer-center": true, "footer-right": true,
	"header-font-size": true, "footer-font-size": true,
	"header-font-name": true, "footer-font-name": true,
	"header-line": true, "footer-line": true,
	"header-spacing": true, "footer-spacing": true,
	"replace":         true,
	"toc-header-text": true,
	"toc-style-sheet": true,
}

type placed struct {
	kind        string
	input       string
	pageSize    string
	pageSizeSet bool
	pageW       string
	pageH       string
	pageWSet    bool
	pageHSet    bool
	orientation string
	orientSet   bool
	zoom        string
	zoomSet     bool
	dpi         string
	dpiSet      bool
	marginSet   bool
	margin      [4]string
	marginTouch [4]bool
	background  *bool
	mediaPrint  *bool
	smartShrink *bool
	preferCSS   bool
	preferSet   bool
	viewport    string
	viewportSet bool
	header      printkit.HeaderFooter
	footer      printkit.HeaderFooter
	headerFile  string
	footerFile  string
	headerTouch bool
	footerTouch bool
	replaces    map[string]string
	tocTitle    string
	tocTitleSet bool
	tocTemplate string
}

func (o placed) hasPage() bool {
	return o.pageSizeSet || o.pageWSet || o.pageHSet || o.orientSet || o.zoomSet || o.dpiSet ||
		o.marginSet || o.background != nil || o.mediaPrint != nil || o.smartShrink != nil ||
		o.preferSet || o.viewportSet
}

func finishEvents(mode string, events []argEvent) (Command, error) {
	d := newDraft(mode)
	words := []int{}
	for i, e := range events {
		if e.flag == "" {
			words = append(words, i)
		}
	}
	last := len(events)
	if len(words) > 0 {
		last = words[len(words)-1]
	}
	var objs []placed
	cur := -1
	expect := ""
	for i, e := range events {
		if e.flag == "help" {
			d.help = true
		}
		if e.flag == "version" {
			d.version = true
		}
		if e.flag == "dump-default-toc" {
			d.dumpTOC = true
		}
		if i == last && e.flag == "" {
			if expect != "" {
				return Command{}, usagef("%s needs a document", expect)
			}
			cur = -1
			continue
		}
		if e.flag != "" {
			if cur >= 0 && pageScoped[e.flag] {
				if err := objs[cur].apply(e.flag, e.vals); err != nil {
					return Command{}, err
				}
			} else if err := d.apply(e.flag, e.vals); err != nil {
				return Command{}, err
			}
			continue
		}
		if expect != "" {
			objs[cur].input = e.word
			expect = ""
			continue
		}
		next := placed{}
		switch e.word {
		case "cover":
			next.kind = "cover"
			expect = "cover"
		case "toc":
			next.kind = "toc"
		case "page":
			next.kind = "page"
			expect = "page"
		default:
			next.kind = "page"
			next.input = e.word
		}
		objs = append(objs, next)
		cur = len(objs) - 1
	}
	if expect != "" {
		return Command{}, usagef("%s needs a document", expect)
	}
	if d.help || d.version || d.dumpTOC {
		return d.finish("", nil)
	}
	if len(words) < 2 {
		return Command{}, usagef("need an input and an output path")
	}
	return d.finish(events[last].word, objs)
}

func (o *placed) apply(name string, vals []string) error {
	one := ""
	if len(vals) > 0 {
		one = vals[0]
	}
	switch name {
	case "page-size":
		o.pageSize, o.pageSizeSet = one, true
	case "page-width":
		o.pageW, o.pageWSet = one, true
	case "page-height":
		o.pageH, o.pageHSet = one, true
	case "orientation":
		o.orientation, o.orientSet = one, true
	case "zoom":
		o.zoom, o.zoomSet = one, true
	case "dpi":
		o.dpi, o.dpiSet = one, true
	case "margin-top":
		o.marginSet, o.marginTouch[0], o.margin[0] = true, true, one
	case "margin-right":
		o.marginSet, o.marginTouch[1], o.margin[1] = true, true, one
	case "margin-bottom":
		o.marginSet, o.marginTouch[2], o.margin[2] = true, true, one
	case "margin-left":
		o.marginSet, o.marginTouch[3], o.margin[3] = true, true, one
	case "print-media-type":
		v := true
		o.mediaPrint = &v
	case "no-print-media-type":
		v := false
		o.mediaPrint = &v
	case "background":
		v := true
		o.background = &v
	case "no-background":
		v := false
		o.background = &v
	case "disable-smart-shrinking":
		v := false
		o.smartShrink = &v
	case "enable-smart-shrinking":
		v := true
		o.smartShrink = &v
	case "prefer-css-page-size":
		o.preferCSS, o.preferSet = true, true
	case "viewport-size":
		o.viewport, o.viewportSet = one, true
	case "header-html":
		o.headerFile, o.headerTouch = one, true
	case "footer-html":
		o.footerFile, o.footerTouch = one, true
	case "header-left":
		o.header.Left, o.headerTouch = one, true
	case "header-center":
		o.header.Center, o.headerTouch = one, true
	case "header-right":
		o.header.Right, o.headerTouch = one, true
	case "footer-left":
		o.footer.Left, o.footerTouch = one, true
	case "footer-center":
		o.footer.Center, o.footerTouch = one, true
	case "footer-right":
		o.footer.Right, o.footerTouch = one, true
	case "header-font-size":
		n, err := strconv.ParseFloat(one, 64)
		if err != nil || n <= 0 {
			return usagef("--header-font-size expects a point size")
		}
		o.header.FontSize, o.headerTouch = n, true
	case "footer-font-size":
		n, err := strconv.ParseFloat(one, 64)
		if err != nil || n <= 0 {
			return usagef("--footer-font-size expects a point size")
		}
		o.footer.FontSize, o.footerTouch = n, true
	case "header-font-name":
		o.header.FontName, o.headerTouch = one, true
	case "footer-font-name":
		o.footer.FontName, o.footerTouch = one, true
	case "header-line":
		o.header.Line, o.headerTouch = true, true
	case "footer-line":
		o.footer.Line, o.footerTouch = true, true
	case "header-spacing":
		n, err := printkit.ParseLength(one)
		if err != nil {
			return usagef("--header-spacing: %s", err)
		}
		o.header.Spacing, o.headerTouch = n, true
	case "footer-spacing":
		n, err := printkit.ParseLength(one)
		if err != nil {
			return usagef("--footer-spacing: %s", err)
		}
		o.footer.Spacing, o.footerTouch = n, true
	case "replace":
		if o.replaces == nil {
			o.replaces = map[string]string{}
		}
		o.replaces[vals[0]] = vals[1]
		o.headerTouch, o.footerTouch = true, true
	case "toc-header-text":
		o.tocTitle, o.tocTitleSet = one, true
	case "toc-style-sheet":
		text, err := readTOCTemplate(one)
		if err != nil {
			return err
		}
		o.tocTemplate = text
	default:
		return usagef("unknown flag --%s", name)
	}
	return nil
}

func (o placed) applyPage(base printkit.Page) (printkit.Page, error) {
	page := base
	if o.pageSizeSet {
		page.Size = printkit.Paper(o.pageSize)
		page.Width, page.Height = printkit.Length{}, printkit.Length{}
	}
	if o.pageWSet || o.pageHSet {
		if !o.pageWSet || !o.pageHSet || o.pageW == "" || o.pageH == "" {
			return page, usagef("--page-width and --page-height must be set together")
		}
		w, err := printkit.ParseLength(o.pageW)
		if err != nil {
			return page, usagef("--page-width: %s", err)
		}
		h, err := printkit.ParseLength(o.pageH)
		if err != nil {
			return page, usagef("--page-height: %s", err)
		}
		page.Width, page.Height = w, h
	}
	if o.orientSet {
		switch strings.ToLower(o.orientation) {
		case "portrait":
			page.Orientation = printkit.Portrait
		case "landscape":
			page.Orientation = printkit.Landscape
		default:
			return page, usagef("--orientation must be Portrait or Landscape")
		}
	}
	if o.zoomSet {
		z, err := strconv.ParseFloat(o.zoom, 64)
		if err != nil || z <= 0 {
			return page, usagef("--zoom expects a positive number")
		}
		page.Scale = z
	}
	if o.dpiSet {
		n, err := strconv.ParseFloat(o.dpi, 64)
		if err != nil || n <= 0 {
			return page, usagef("--dpi expects a positive number")
		}
		page.DPI = n
	}
	if o.mediaPrint != nil {
		if *o.mediaPrint {
			page.Media = printkit.MediaPrint
		} else {
			page.Media = printkit.MediaScreen
		}
	}
	if o.smartShrink != nil {
		page.SmartShrinking = *o.smartShrink
	}
	if o.background != nil {
		page.PrintBackground = o.background
	}
	if o.preferSet {
		page.PreferCSSPageSize = o.preferCSS
	}
	if o.marginSet {
		sides := [4]string{"10mm", "10mm", "10mm", "10mm"}
		if base.Margin.Custom() {
			sides[0] = base.Margin.Top.String()
			sides[1] = base.Margin.Right.String()
			sides[2] = base.Margin.Bottom.String()
			sides[3] = base.Margin.Left.String()
		}
		for i := 0; i < 4; i++ {
			if o.marginTouch[i] {
				sides[i] = o.margin[i]
			}
		}
		var lengths [4]printkit.Length
		for i, raw := range sides {
			if raw == "" {
				raw = "10mm"
			}
			n, err := printkit.ParseLength(raw)
			if err != nil {
				return page, usagef("margin: %s", err)
			}
			lengths[i] = n
		}
		page.Margin = printkit.Margins(lengths[0], lengths[1], lengths[2], lengths[3])
	}
	if o.viewportSet {
		w, h, err := parseViewport(o.viewport)
		if err != nil {
			return page, err
		}
		page.ViewportWidth, page.ViewportHeight = w, h
	}
	return page, nil
}

func (d *draft) decorate(obj placed, cover bool) (printkit.Input, error) {
	in := inputFrom(obj.input)
	base, err := d.page()
	if err != nil {
		return in, err
	}
	if obj.hasPage() {
		page, err := obj.applyPage(base)
		if err != nil {
			return in, err
		}
		in.Page = &page
	}
	globalHeader := d.header
	globalFooter := d.footer
	if d.headerFile != "" {
		globalHeader.File = d.headerFile
	}
	if d.footerFile != "" {
		globalFooter.File = d.footerFile
	}
	if len(d.replaces) > 0 {
		globalHeader.Replace = d.replaces
		globalFooter.Replace = d.replaces
	}
	if cover {
		if obj.headerTouch {
			h := mergeHeader(printkit.HeaderFooter{}, obj, true)
			in.Header = &h
		}
		if obj.footerTouch {
			f := mergeHeader(printkit.HeaderFooter{}, obj, false)
			in.Footer = &f
		}
		return in, nil
	}
	if obj.headerTouch {
		h := mergeHeader(globalHeader, obj, true)
		in.Header = &h
	}
	if obj.footerTouch {
		f := mergeHeader(globalFooter, obj, false)
		in.Footer = &f
	}
	return in, nil
}

func mergeHeader(base printkit.HeaderFooter, obj placed, header bool) printkit.HeaderFooter {
	h := base
	if header {
		if obj.headerFile != "" {
			h.File = obj.headerFile
			h.HTML, h.Left, h.Center, h.Right = "", "", "", ""
		}
		if obj.header.Left != "" {
			h.Left = obj.header.Left
		}
		if obj.header.Center != "" {
			h.Center = obj.header.Center
		}
		if obj.header.Right != "" {
			h.Right = obj.header.Right
		}
		if obj.header.FontSize > 0 {
			h.FontSize = obj.header.FontSize
		}
		if obj.header.FontName != "" {
			h.FontName = obj.header.FontName
		}
		if obj.header.Line {
			h.Line = true
		}
		if !obj.header.Spacing.IsZero() {
			h.Spacing = obj.header.Spacing
		}
	} else {
		if obj.footerFile != "" {
			h.File = obj.footerFile
			h.HTML, h.Left, h.Center, h.Right = "", "", "", ""
		}
		if obj.footer.Left != "" {
			h.Left = obj.footer.Left
		}
		if obj.footer.Center != "" {
			h.Center = obj.footer.Center
		}
		if obj.footer.Right != "" {
			h.Right = obj.footer.Right
		}
		if obj.footer.FontSize > 0 {
			h.FontSize = obj.footer.FontSize
		}
		if obj.footer.FontName != "" {
			h.FontName = obj.footer.FontName
		}
		if obj.footer.Line {
			h.Line = true
		}
		if !obj.footer.Spacing.IsZero() {
			h.Spacing = obj.footer.Spacing
		}
	}
	if len(obj.replaces) > 0 {
		merged := map[string]string{}
		for k, v := range h.Replace {
			merged[k] = v
		}
		for k, v := range obj.replaces {
			merged[k] = v
		}
		h.Replace = merged
	}
	return h
}

func readTOCTemplate(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", usagef("--toc-style-sheet: %s", err)
	}
	if strings.Contains(strings.ToLower(string(b)), "<xsl:") {
		return "", usagef("--toc-style-sheet is an HTML template, not XSLT")
	}
	return string(b), nil
}
