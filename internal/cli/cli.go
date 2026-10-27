// Package cli parses wkhtmltopdf-style arguments and runs a printkit conversion.
// It is the implementation of cmd/htmlprint. Library users should call printkit
// directly.
package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tushar1210/printkit"
)

// Run parses args and writes a PDF or an image. argv0 is the program name and
// selects the default mode: a name containing "wkhtmltoimage" defaults to an
// image, anything else defaults to PDF. The leading "pdf" or "image" argument
// overrides that. Exit status 0 is success, 2 is a usage error, and 1 is a
// failed conversion.
func Run(argv0 string, args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	cmd, err := Parse(argv0, args)
	if err != nil {
		writef(stderr, "htmlprint: %s\n", err)
		if _, ok := err.(*usageError); ok {
			writef(stderr, "htmlprint: try htmlprint --help\n")
			return 2
		}
		return 2
	}
	if cmd.Help {
		writef(stdout, "%s", Help(cmd.Mode))
		return 0
	}
	if cmd.Version {
		writef(stdout, "htmlprint %s\n", printkit.Version)
		return 0
	}
	if cmd.DumpTOC {
		writef(stdout, "%s\n", printkit.DefaultTOCTemplate)
		return 0
	}
	ctx := context.Background()
	conv, err := printkit.New(cmd.Options...)
	if err != nil {
		writef(stderr, "htmlprint: %s\n", err)
		return 1
	}
	defer func() { _ = conv.Close() }()
	switch cmd.Mode {
	case "image":
		err = writeImage(ctx, conv, cmd, stdout)
	default:
		err = writePDF(ctx, conv, cmd, stdout)
	}
	if err != nil {
		writef(stderr, "htmlprint: %s\n", err)
		return 1
	}
	return 0
}

func writef(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, format, args...)
}

func writePDF(ctx context.Context, conv *printkit.Converter, cmd Command, stdout io.Writer) error {
	if cmd.Output == "-" {
		return conv.WritePDF(ctx, stdout, cmd.PDF)
	}
	return conv.SavePDF(ctx, cmd.Output, cmd.PDF)
}

func writeImage(ctx context.Context, conv *printkit.Converter, cmd Command, stdout io.Writer) error {
	if cmd.Output == "-" {
		return conv.WriteImage(ctx, stdout, cmd.Image)
	}
	return conv.SaveImage(ctx, cmd.Output, cmd.Image)
}

// Command is a parsed invocation.
type Command struct {
	Mode    string
	Help    bool
	Version bool
	Quiet   bool
	DumpTOC bool
	Output  string
	PDF     printkit.Request
	Image   printkit.ImageRequest
	Options []printkit.Option
}

// Parse converts arguments into a Command. It does not launch Chromium.
func Parse(argv0 string, args []string) (Command, error) {
	mode := defaultMode(argv0)
	if len(args) > 0 && (args[0] == "pdf" || args[0] == "image") {
		mode = args[0]
		args = args[1:]
	}
	events, err := scanArgs(mode, args)
	if err != nil {
		return Command{}, err
	}
	return finishEvents(mode, events)
}

func defaultMode(argv0 string) string {
	base := strings.ToLower(filepath.Base(argv0))
	if strings.Contains(base, "wkhtmltoimage") || strings.Contains(base, "htmltoimage") {
		return "image"
	}
	return "pdf"
}

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

type flagSpec struct {
	arity int
	modes string // "p" pdf, "i" image, "pi" both
}

var flagSpecs = map[string]flagSpec{
	"help": {}, "version": {}, "quiet": {},
	"page-size": {1, "p"}, "page-width": {1, "p"}, "page-height": {1, "p"},
	"orientation": {1, "p"},
	"margin-top":  {1, "p"}, "margin-right": {1, "p"}, "margin-bottom": {1, "p"}, "margin-left": {1, "p"},
	"zoom": {1, "pi"}, "dpi": {1, "pi"},
	"grayscale": {}, "print-media-type": {}, "no-print-media-type": {},
	"background": {}, "no-background": {},
	"disable-smart-shrinking": {}, "enable-smart-shrinking": {},
	"javascript-delay": {1, "pi"}, "window-status": {1, "pi"},
	"disable-javascript": {}, "enable-javascript": {},
	"load-error-handling": {1, "pi"}, "load-media-error-handling": {1, "pi"},
	"custom-header": {2, "pi"}, "custom-header-propagation": {},
	"cookie": {2, "pi"}, "username": {1, "pi"}, "password": {1, "pi"},
	"proxy": {1, "pi"}, "user-agent": {1, "pi"},
	"enable-local-file-access": {}, "disable-local-file-access": {},
	"allow":            {1, "pi"},
	"header-html":      {1, "p"},
	"footer-html":      {1, "p"},
	"header-left":      {1, "p"},
	"header-center":    {1, "p"},
	"header-right":     {1, "p"},
	"footer-left":      {1, "p"},
	"footer-center":    {1, "p"},
	"footer-right":     {1, "p"},
	"header-font-size": {1, "p"}, "footer-font-size": {1, "p"},
	"header-font-name": {1, "p"}, "footer-font-name": {1, "p"},
	"header-line": {}, "footer-line": {},
	"header-spacing": {1, "p"}, "footer-spacing": {1, "p"},
	"replace": {2, "p"},
	"title":   {1, "p"}, "author": {1, "p"}, "subject": {1, "p"}, "keywords": {1, "p"},
	"outline": {}, "no-outline": {}, "outline-depth": {1, "p"},
	"toc": {}, "toc-header-text": {1, "p"}, "cover": {1, "p"},
	"user-style-sheet": {1, "pi"}, "run-script": {1, "pi"},
	"viewport-size": {1, "p"},
	"images":        {}, "no-images": {},
	"disable-external-links": {}, "disable-internal-links": {},
	"page-offset": {1, "p"}, "encoding": {1, "pi"},
	"chrome-path": {1, "pi"}, "no-sandbox": {}, "sandbox": {},
	"wait-for": {1, "pi"}, "wait-network-idle": {1, "pi"}, "timeout": {1, "pi"},
	"lowquality": {},
	"format":     {1, "i"}, "quality": {1, "i"},
	"width": {1, "i"}, "height": {1, "i"},
	"crop-x": {1, "i"}, "crop-y": {1, "i"}, "crop-w": {1, "i"}, "crop-h": {1, "i"},
	"transparent":          {},
	"disable-smart-width":  {},
	"enable-smart-width":   {},
	"prefer-css-page-size": {},
	"copies":               {1, "p"},
	"collate":              {0, "p"},
	"no-collate":           {0, "p"},
	"minimum-font-size":    {1, "pi"},
	"toc-style-sheet":      {1, "p"},
	"dump-default-toc":     {},
	"tagged-pdf":           {0, "p"},
}

func init() {
	// Flags listed with arity 0 above still need a mode. Fill "pi" unless a
	// mode was set explicitly by a composite literal that included modes.
	for name, spec := range flagSpecs {
		if spec.modes == "" {
			spec.modes = "pi"
			flagSpecs[name] = spec
		}
	}
	// These are PDF-only even though they take no value.
	for _, name := range []string{
		"grayscale", "print-media-type", "no-print-media-type",
		"background", "no-background", "disable-smart-shrinking", "enable-smart-shrinking",
		"header-line", "footer-line", "outline", "no-outline", "toc",
		"disable-external-links", "disable-internal-links", "prefer-css-page-size",
		"lowquality",
	} {
		spec := flagSpecs[name]
		if name == "grayscale" {
			continue
		}
		spec.modes = "p"
		flagSpecs[name] = spec
	}
	for _, name := range []string{"transparent", "disable-smart-width", "enable-smart-width"} {
		spec := flagSpecs[name]
		spec.modes = "i"
		flagSpecs[name] = spec
	}
	// chrome-path was given arity 0 by the composite above because the literal
	// omitted the arity. Set the arities that the map literal left at zero by
	// mistake for value flags. The map literal is the source of truth; this
	// init only fills modes.
}

// unsupported flags fail the parse so a script does not silently change behavior.
var unsupported = map[string]string{
	"enable-forms":            "Chromium printToPDF paints a static page and has no switch that emits AcroForm fields",
	"checkbox-svg":            "Chromium printToPDF does not emit AcroForm widgets",
	"checkbox-checked-svg":    "Chromium printToPDF does not emit AcroForm widgets",
	"radiobutton-svg":         "Chromium printToPDF does not emit AcroForm widgets",
	"radiobutton-checked-svg": "Chromium printToPDF does not emit AcroForm widgets",
	"debug-javascript":        "use the converter log or DevTools on the page instead",
	"no-stop-slow-scripts":    "script deadlines follow the conversion timeout",
	"stop-slow-scripts":       "script deadlines follow the conversion timeout",
	"dump-outline":            "read the outline from the PDF instead",
	"read-args-from-stdin":    "pass arguments on the command line",
	"cache-dir":               "Chromium uses a temporary profile",
	"enable-plugins":          "Chromium plugins are not available",
	"disable-plugins":         "Chromium plugins are not available",
	"keep-relative-links":     "links are resolved by Chromium while the page loads",
	"resolve-relative-links":  "links are resolved by Chromium while the page loads",
	"exclude-from-outline":    "outline depth is global; use --outline-depth",
	"include-in-outline":      "outline depth is global; use --outline-depth",
	"disable-dotted-lines":    "link appearance follows Chromium",
	"disable-toc-back-links":  "table-of-contents back links are not generated",
	"footer-html-extra":       "use --footer-html",
	"header-html-extra":       "use --header-html",
	"page-offset-extra":       "use --page-offset",
	"xsl-style-sheet":         "printkit does not run XSLT; pass an HTML template to --toc-style-sheet",
	"dump-default-toc-xsl":    "the default table of contents is HTML; use --dump-default-toc",
}

type draft struct {
	mode string

	help, version, quiet bool
	chrome               string
	noSandbox            *bool
	timeout              time.Duration

	pageSize, pageW, pageH, orientation, zoom, dpi string
	marginSet                                      bool
	margin                                         [4]string
	background                                     *bool
	mediaPrint                                     *bool
	smartShrink                                    *bool
	grayscale                                      bool
	preferCSS                                      bool
	viewport                                       string

	header, footer     printkit.HeaderFooter
	headerFile         string
	footerFile         string
	replaces           map[string]string
	title              string
	meta               printkit.Metadata
	outline            *bool
	outlineDepth       int
	toc                bool
	tocTitle           string
	cover              string
	external, internal bool
	pageOffset         int
	copies             int
	collate            *bool
	minFont            float64
	tagged             bool
	tocTemplate        string
	dumpTOC            bool

	delay        time.Duration
	windowStatus string
	selector     string
	idle         time.Duration
	load         string
	headers      map[string]string
	cookies      []printkit.Cookie
	username     string
	password     string
	proxy        string
	userAgent    string
	fileAccess   *bool
	allow        []string
	js           *bool
	images       *bool
	userCSS      string
	script       string
	encoding     string

	format      string
	quality     int
	width       int
	height      int
	crop        [4]float64
	cropSet     [4]bool
	transparent bool
	smartWidth  *bool
}

func newDraft(mode string) *draft {
	return &draft{
		mode:     mode,
		headers:  map[string]string{},
		replaces: map[string]string{},
	}
}

func (d *draft) apply(name string, vals []string) error {
	one := ""
	if len(vals) > 0 {
		one = vals[0]
	}
	switch name {
	case "help":
		d.help = true
	case "version":
		d.version = true
	case "quiet":
		d.quiet = true
	case "lowquality", "custom-header-propagation":
	case "page-size":
		d.pageSize = one
	case "page-width":
		d.pageW = one
	case "page-height":
		d.pageH = one
	case "orientation":
		d.orientation = one
	case "margin-top":
		d.marginSet = true
		d.margin[0] = one
	case "margin-right":
		d.marginSet = true
		d.margin[1] = one
	case "margin-bottom":
		d.marginSet = true
		d.margin[2] = one
	case "margin-left":
		d.marginSet = true
		d.margin[3] = one
	case "zoom":
		d.zoom = one
	case "dpi":
		d.dpi = one
	case "grayscale":
		d.grayscale = true
	case "print-media-type":
		v := true
		d.mediaPrint = &v
	case "no-print-media-type":
		v := false
		d.mediaPrint = &v
	case "background":
		v := true
		d.background = &v
	case "no-background":
		v := false
		d.background = &v
	case "disable-smart-shrinking":
		v := false
		d.smartShrink = &v
	case "enable-smart-shrinking":
		v := true
		d.smartShrink = &v
	case "javascript-delay":
		ms, err := strconv.Atoi(one)
		if err != nil || ms < 0 {
			return usagef("--javascript-delay expects milliseconds")
		}
		d.delay = time.Duration(ms) * time.Millisecond
	case "window-status":
		d.windowStatus = one
	case "disable-javascript":
		v := false
		d.js = &v
	case "enable-javascript":
		v := true
		d.js = &v
	case "load-error-handling", "load-media-error-handling":
		switch strings.ToLower(one) {
		case "abort", "ignore", "skip":
			d.load = strings.ToLower(one)
		default:
			return usagef("--%s must be abort, ignore, or skip", name)
		}
	case "custom-header":
		d.headers[vals[0]] = vals[1]
	case "cookie":
		d.cookies = append(d.cookies, printkit.Cookie{Name: vals[0], Value: vals[1]})
	case "username":
		d.username = one
	case "password":
		d.password = one
	case "proxy":
		d.proxy = one
	case "user-agent":
		d.userAgent = one
	case "enable-local-file-access":
		v := true
		d.fileAccess = &v
	case "disable-local-file-access":
		v := false
		d.fileAccess = &v
	case "allow":
		d.allow = append(d.allow, one)
	case "header-html":
		d.headerFile = one
	case "footer-html":
		d.footerFile = one
	case "header-left":
		d.header.Left = one
	case "header-center":
		d.header.Center = one
	case "header-right":
		d.header.Right = one
	case "footer-left":
		d.footer.Left = one
	case "footer-center":
		d.footer.Center = one
	case "footer-right":
		d.footer.Right = one
	case "header-font-size":
		n, err := strconv.ParseFloat(one, 64)
		if err != nil || n <= 0 {
			return usagef("--header-font-size expects a point size")
		}
		d.header.FontSize = n
	case "footer-font-size":
		n, err := strconv.ParseFloat(one, 64)
		if err != nil || n <= 0 {
			return usagef("--footer-font-size expects a point size")
		}
		d.footer.FontSize = n
	case "header-font-name":
		d.header.FontName = one
	case "footer-font-name":
		d.footer.FontName = one
	case "header-line":
		d.header.Line = true
	case "footer-line":
		d.footer.Line = true
	case "header-spacing":
		n, err := printkit.ParseLength(one)
		if err != nil {
			return usagef("--header-spacing: %s", err)
		}
		d.header.Spacing = n
	case "footer-spacing":
		n, err := printkit.ParseLength(one)
		if err != nil {
			return usagef("--footer-spacing: %s", err)
		}
		d.footer.Spacing = n
	case "replace":
		d.replaces[vals[0]] = vals[1]
	case "title":
		d.title = one
		d.meta.Title = one
	case "author":
		d.meta.Author = one
	case "subject":
		d.meta.Subject = one
	case "keywords":
		d.meta.Keywords = one
	case "outline":
		v := true
		d.outline = &v
	case "no-outline":
		v := false
		d.outline = &v
	case "outline-depth":
		n, err := strconv.Atoi(one)
		if err != nil || n < 0 {
			return usagef("--outline-depth expects a non-negative integer")
		}
		d.outlineDepth = n
	case "toc":
		d.toc = true
	case "toc-header-text":
		d.toc = true
		d.tocTitle = one
	case "cover":
		d.cover = one
	case "user-style-sheet":
		d.userCSS = one
	case "run-script":
		d.script = one
	case "viewport-size":
		d.viewport = one
	case "images":
		v := true
		d.images = &v
	case "no-images":
		v := false
		d.images = &v
	case "disable-external-links":
		d.external = true
	case "disable-internal-links":
		d.internal = true
	case "page-offset":
		n, err := strconv.Atoi(one)
		if err != nil {
			return usagef("--page-offset expects an integer")
		}
		d.pageOffset = n
	case "encoding":
		d.encoding = one
	case "chrome-path":
		d.chrome = one
	case "no-sandbox":
		v := true
		d.noSandbox = &v
	case "sandbox":
		v := false
		d.noSandbox = &v
	case "wait-for":
		d.selector = one
	case "wait-network-idle":
		dur, err := parseMillisOrDuration(one)
		if err != nil {
			return usagef("--wait-network-idle: %s", err)
		}
		d.idle = dur
	case "timeout":
		dur, err := parseSecondsOrDuration(one)
		if err != nil {
			return usagef("--timeout: %s", err)
		}
		d.timeout = dur
	case "format":
		d.format = one
	case "quality":
		n, err := strconv.Atoi(one)
		if err != nil || n < 1 || n > 100 {
			return usagef("--quality must be from 1 to 100")
		}
		d.quality = n
	case "width":
		n, err := strconv.Atoi(one)
		if err != nil || n < 1 {
			return usagef("--width expects a positive pixel count")
		}
		d.width = n
	case "height":
		n, err := strconv.Atoi(one)
		if err != nil || n < 1 {
			return usagef("--height expects a positive pixel count")
		}
		d.height = n
	case "crop-x":
		return d.cropAt(0, one, name)
	case "crop-y":
		return d.cropAt(1, one, name)
	case "crop-w":
		return d.cropAt(2, one, name)
	case "crop-h":
		return d.cropAt(3, one, name)
	case "transparent":
		d.transparent = true
	case "disable-smart-width":
		v := false
		d.smartWidth = &v
	case "enable-smart-width":
		v := true
		d.smartWidth = &v
	case "prefer-css-page-size":
		d.preferCSS = true
	case "copies":
		n, err := strconv.Atoi(one)
		if err != nil || n < 1 {
			return usagef("--copies expects a positive integer")
		}
		d.copies = n
	case "collate":
		v := true
		d.collate = &v
	case "no-collate":
		v := false
		d.collate = &v
	case "minimum-font-size":
		n, err := strconv.ParseFloat(one, 64)
		if err != nil || n < 0 {
			return usagef("--minimum-font-size expects a point size")
		}
		d.minFont = n
	case "toc-style-sheet":
		text, err := readTOCTemplate(one)
		if err != nil {
			return err
		}
		d.tocTemplate = text
		d.toc = true
	case "dump-default-toc":
		d.dumpTOC = true
	case "tagged-pdf":
		d.tagged = true
	default:
		return usagef("unknown flag --%s", name)
	}
	return nil
}

func (d *draft) cropAt(i int, raw, name string) error {
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return usagef("--%s expects a number of CSS pixels", name)
	}
	d.crop[i] = n
	d.cropSet[i] = true
	return nil
}

func (d *draft) finish(output string, objs []placed) (Command, error) {
	cmd := Command{Mode: d.mode, Help: d.help, Version: d.version, Quiet: d.quiet, DumpTOC: d.dumpTOC}
	if d.help || d.version || d.dumpTOC {
		return cmd, nil
	}
	if d.encoding != "" && !strings.EqualFold(d.encoding, "utf-8") && !strings.EqualFold(d.encoding, "utf8") {
		return Command{}, usagef("--encoding %q is not supported; printkit reads UTF-8", d.encoding)
	}
	if output == "" || len(objs) == 0 {
		return Command{}, usagef("need an input and an output path")
	}
	cmd.Output = output

	page, err := d.page()
	if err != nil {
		return Command{}, err
	}
	wait := printkit.Wait{
		Delay:        d.delay,
		NetworkIdle:  d.idle,
		Selector:     d.selector,
		WindowStatus: d.windowStatus,
		Timeout:      d.timeout,
	}
	net := printkit.Network{
		Headers:         d.headers,
		Cookies:         d.cookies,
		Username:        d.username,
		Password:        d.password,
		Proxy:           d.proxy,
		UserAgent:       d.userAgent,
		AllowFileAccess: d.fileAccess,
		AllowPaths:      d.allow,
		JavaScript:      d.js,
		Images:          d.images,
		Timeout:         d.timeout,
	}
	inj := printkit.Inject{UserStyleSheet: d.userCSS, JavaScript: d.script}
	if len(d.replaces) > 0 {
		d.header.Replace = d.replaces
		d.footer.Replace = d.replaces
	}
	if d.headerFile != "" {
		d.header.File = d.headerFile
	}
	if d.footerFile != "" {
		d.footer.File = d.footerFile
	}

	switch d.mode {
	case "image":
		if len(objs) != 1 || objs[0].kind != "page" {
			return Command{}, usagef("image mode takes one input document")
		}
		img := printkit.ImageRequest{
			Input:           inputFrom(objs[0].input),
			Format:          printkit.ImageFormat(strings.ToLower(d.format)),
			Quality:         d.quality,
			Width:           d.width,
			Height:          d.height,
			OmitBackground:  d.transparent,
			Media:           page.Media,
			Grayscale:       d.grayscale,
			Scale:           page.Scale,
			DPI:             page.DPI,
			Wait:            wait,
			Network:         net,
			Inject:          inj,
			MinimumFontSize: d.minFont,
		}
		if d.smartWidth == nil || *d.smartWidth {
			img.SmartWidth = true
		}
		if d.cropSet[0] || d.cropSet[1] || d.cropSet[2] || d.cropSet[3] {
			if !d.cropSet[2] || !d.cropSet[3] || d.crop[2] <= 0 || d.crop[3] <= 0 {
				return Command{}, usagef("--crop-w and --crop-h must be positive when cropping")
			}
			img.Clip = &printkit.Clip{X: d.crop[0], Y: d.crop[1], Width: d.crop[2], Height: d.crop[3]}
		}
		cmd.Image = img
	default:
		var inputs []printkit.Input
		cover := d.cover
		var coverIn *printkit.Input
		toc := d.toc
		var tocPage *printkit.Page
		tocTitle := d.tocTitle
		tocTemplate := d.tocTemplate
		for _, obj := range objs {
			switch obj.kind {
			case "cover":
				if obj.input == "" {
					return Command{}, usagef("cover needs a document")
				}
				cover = obj.input
				in, err := d.decorate(obj, true)
				if err != nil {
					return Command{}, err
				}
				coverIn = &in
			case "toc":
				toc = true
				if obj.tocTitleSet {
					tocTitle = obj.tocTitle
				}
				if obj.tocTemplate != "" {
					tocTemplate = obj.tocTemplate
				}
				if obj.hasPage() {
					p, err := obj.applyPage(page)
					if err != nil {
						return Command{}, err
					}
					tocPage = &p
				}
			default:
				in, err := d.decorate(obj, false)
				if err != nil {
					return Command{}, err
				}
				inputs = append(inputs, in)
			}
		}
		if len(inputs) == 0 && cover == "" {
			return Command{}, usagef("need an input document")
		}
		req := printkit.Request{
			Inputs:               inputs,
			Page:                 page,
			Metadata:             d.meta,
			Wait:                 wait,
			Network:              net,
			Inject:               inj,
			Grayscale:            d.grayscale,
			PageOffset:           d.pageOffset,
			DisableExternalLinks: d.external,
			DisableInternalLinks: d.internal,
			Copies:               d.copies,
			Collate:              d.collate,
			MinimumFontSize:      d.minFont,
			Tagged:               d.tagged,
		}
		if d.load != "" {
			req.LoadErrors = printkit.LoadErrorPolicy(d.load)
		}
		if d.outline != nil {
			req.Outline.Enabled = d.outline
		}
		req.Outline.Depth = d.outlineDepth
		if headerActiveDraft(d.header) {
			h := d.header
			req.Header = &h
		}
		if headerActiveDraft(d.footer) {
			f := d.footer
			req.Footer = &f
		}
		if toc {
			req.TOC = &printkit.TOC{Title: tocTitle, Template: tocTemplate, Page: tocPage}
		}
		if coverIn != nil {
			req.Cover = coverIn
		} else if cover != "" {
			in := inputFrom(cover)
			req.Cover = &in
		}
		cmd.PDF = req
	}
	limit := 2 * time.Minute
	if d.timeout > 0 {
		limit = d.timeout
	}
	cmd.Options = append(cmd.Options, printkit.WithDefaultTimeout(limit))
	if d.chrome != "" {
		cmd.Options = append(cmd.Options, printkit.WithChromePath(d.chrome))
	}
	if d.noSandbox != nil {
		cmd.Options = append(cmd.Options, printkit.WithNoSandbox(*d.noSandbox))
	}
	if d.proxy != "" {
		cmd.Options = append(cmd.Options, printkit.WithProxy(d.proxy))
	}
	return cmd, nil
}

func (d *draft) page() (printkit.Page, error) {
	page := printkit.Page{Media: printkit.MediaScreen, SmartShrinking: true}
	if d.mediaPrint != nil && *d.mediaPrint {
		page.Media = printkit.MediaPrint
	}
	if d.smartShrink != nil {
		page.SmartShrinking = *d.smartShrink
	}
	off := false
	page.PrintBackground = &off
	if d.background != nil {
		page.PrintBackground = d.background
	}
	page.PreferCSSPageSize = d.preferCSS
	if d.pageSize != "" {
		page.Size = printkit.Paper(d.pageSize)
	}
	if d.pageW != "" || d.pageH != "" {
		if d.pageW == "" || d.pageH == "" {
			return page, usagef("--page-width and --page-height must be set together")
		}
		w, err := printkit.ParseLength(d.pageW)
		if err != nil {
			return page, usagef("--page-width: %s", err)
		}
		h, err := printkit.ParseLength(d.pageH)
		if err != nil {
			return page, usagef("--page-height: %s", err)
		}
		page.Width, page.Height = w, h
	}
	switch strings.ToLower(d.orientation) {
	case "", "portrait":
		if d.orientation != "" {
			page.Orientation = printkit.Portrait
		}
	case "landscape":
		page.Orientation = printkit.Landscape
	default:
		return page, usagef("--orientation must be Portrait or Landscape")
	}
	if d.zoom != "" {
		z, err := strconv.ParseFloat(d.zoom, 64)
		if err != nil || z <= 0 {
			return page, usagef("--zoom expects a positive number")
		}
		page.Scale = z
	}
	if d.dpi != "" {
		n, err := strconv.ParseFloat(d.dpi, 64)
		if err != nil || n <= 0 {
			return page, usagef("--dpi expects a positive number")
		}
		page.DPI = n
	}
	if d.marginSet {
		sides := d.margin
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
	if d.viewport != "" {
		w, h, err := parseViewport(d.viewport)
		if err != nil {
			return page, err
		}
		page.ViewportWidth, page.ViewportHeight = w, h
	}
	return page, nil
}

func headerActiveDraft(h printkit.HeaderFooter) bool {
	return h.HTML != "" || h.File != "" || h.Left != "" || h.Center != "" || h.Right != ""
}

func inputFrom(s string) printkit.Input {
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "file://") {
		return printkit.FromURL(s)
	}
	return printkit.FromFile(s)
}

func parseViewport(s string) (int, int, error) {
	w, h, ok := strings.Cut(strings.ToLower(s), "x")
	if !ok {
		return 0, 0, usagef("--viewport-size expects WxH, for example 1280x800")
	}
	wi, err1 := strconv.Atoi(w)
	hi, err2 := strconv.Atoi(h)
	if err1 != nil || err2 != nil || wi < 1 || hi < 1 {
		return 0, 0, usagef("--viewport-size expects WxH, for example 1280x800")
	}
	return wi, hi, nil
}

func parseMillisOrDuration(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("negative duration")
		}
		return time.Duration(n) * time.Millisecond, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("expect milliseconds or a duration such as 500ms")
	}
	return d, nil
}

func parseSecondsOrDuration(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("negative duration")
		}
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("expect seconds or a duration such as 30s")
	}
	return d, nil
}

// Help is the --help text for mode, which is "pdf" or "image".
func Help(mode string) string {
	return helpText
}

const helpText = `htmlprint renders HTML to PDF or an image with headless Chromium.

Usage:
  htmlprint [pdf] [options] [cover <file>] [toc] [page] <input>... <output>
  htmlprint image [options] <input> <output>

The default mode is pdf. A program name containing wkhtmltoimage selects image
mode, so the binary can be installed as wkhtmltopdf or wkhtmltoimage. The
output path "-" writes to standard output.

Inputs are local files, or http(s) and file:// URLs. "cover", "toc", and "page"
are positional keywords, matching wkhtmltopdf.

PDF options:
  --page-size <name>          A0-A10, B0-B10, Letter, Legal, Tabloid, Ledger,
                              Executive, Folio, C5E, Comm10E, DLE (default A4)
  --page-width <len>          Custom width. A bare number is millimeters.
  --page-height <len>         Custom height. Set together with --page-width.
  --orientation Portrait|Landscape
  --margin-top|right|bottom|left <len>
  --zoom <scale>              Print scale, 0.1 to 2.
  --dpi <n>                   Accepted. PDF output is vector, so this does not
                              resample the file. Images use it as device scale.
  --grayscale
  --print-media-type          Lay out with CSS print media. The default is screen,
                              matching wkhtmltopdf.
  --background                Print CSS backgrounds. Off unless this is set.
  --disable-smart-shrinking   Keep scale at --zoom. On by default, matching
                              wkhtmltopdf.
  --prefer-css-page-size      Honor an @page size rule.
  --header-left|center|right <text>
  --footer-left|center|right <text>
  --header-html <file>        HTML header template. Overrides left/center/right.
  --footer-html <file>
  --header-font-size <pt>     Used with left/center/right. Default 9.
  --header-font-name <name>
  --footer-font-size <pt>
  --footer-font-name <name>
  --header-line               Draw a rule under the text header.
  --footer-line
  --header-spacing <len>      Extra gap added to the top margin.
  --footer-spacing <len>
  --replace <name> <value>    Replace [name] in header and footer templates.
  Tokens: [page] [topage] [sitepage] [sitepages] [frompage] [date] [isodate]
  [time] [title] [doctitle] [webpage] [url] [section] [subsection]
  --title <text>              Document title and PDF metadata.
  --author <text>             PDF metadata. Also --subject and --keywords.
  --outline / --no-outline    Bookmarks from headings. On by default.
  --outline-depth <n>
  --toc                         Table of contents from headings.
  --toc-header-text <text>
  --toc-style-sheet <file>    HTML template for the table of contents.
                              Data is Title and Entries (Level, Title, Page, Href, Indent).
  --dump-default-toc          Print the default HTML template and exit.
  --cover <file>                Also accepted as the positional "cover".
  --copies <n>                  Repeat the document n times in one file.
  --collate / --no-collate      With --copies, repeat the document or each page.
  --minimum-font-size <pt>    Raise text smaller than this. Does not shrink larger text.
  --tagged-pdf                  Keep Chromium's accessible structure tree.
                              Off by default so large tables stay small. Outlines stay.
  --page-offset <n>             Added to displayed page numbers.
  --disable-external-links
  --disable-internal-links
  --viewport-size <WxH>

Image options:
  --format png|jpeg|webp      Default png. jpg is accepted as jpeg.
  --quality <1-100>           JPEG and WebP. Default 90.
  --width <px>                Layout width. Default 1024.
  --height <px>               Viewport height. Omit it to capture the full page.
  --crop-x --crop-y --crop-w --crop-h <px>
  --zoom <scale>              Device scale. Not combined with --dpi.
  --dpi <n>                   Device scale n/96 when --zoom is omitted.
  --transparent               Transparent background. PNG and WebP.
  --disable-smart-width       Do not grow the viewport to the document width.
                              Smart width is on by default.

Shared options:
  --javascript-delay <ms>
  --window-status <text>      Wait until window.status equals text.
  --wait-for <css>            Extension. Wait until a selector matches.
  --wait-network-idle <ms|dur>  Extension. Quiet window with no new resources.
  --timeout <sec|dur>         Extension. Bounds the conversion.
  --custom-header <name> <value>
  --cookie <name> <value>
  --username <user> --password <pass>
  --proxy <url> --user-agent <string>
  --disable-javascript / --enable-javascript
  --no-images / --images
  --disable-local-file-access  Reject local files and file:// URLs.
                              Access is allowed unless this is set, which differs
                              from wkhtmltopdf 0.12.6.
  --allow <dir>               Restrict local files to this directory. Repeatable.
  --user-style-sheet <file>
  --run-script <js>
  --load-error-handling abort|ignore|skip
  --encoding utf-8            Any other encoding is rejected.
  --chrome-path <path>        Extension.
  --no-sandbox                Extension. Also implied when running as root.
  --quiet
  --version
  --help

--lowquality and --custom-header-propagation are accepted and do nothing.
Headers are always sent on every request. PDF compression is left to Chromium.

Page size, margins, zoom, background, media, smart shrinking, viewport, and
header/footer flags apply to the object they follow (cover, toc, or page).
Flags before the first object are the defaults. A cover does not inherit the
document header or footer unless those flags are repeated on the cover.

Some wkhtmltopdf flags are rejected on purpose (forms, plugins, XSLT, and
others). The error names the flag and the reason. --xsl-style-sheet is
rejected; use --toc-style-sheet with an HTML template. Interactive form
fields are rejected because Chromium's printToPDF call does not emit AcroForm
widgets.
`
