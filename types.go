package printkit

import (
	"io"
	"time"
)

// Orientation is the page orientation.
type Orientation string

const (
	Portrait  Orientation = "Portrait"
	Landscape Orientation = "Landscape"
)

// Media is the CSS media type used while the document is laid out.
type Media string

const (
	MediaPrint  Media = "print"
	MediaScreen Media = "screen"
)

// LoadErrorPolicy controls what happens when a document fails to load.
type LoadErrorPolicy string

const (
	// LoadErrorAbort stops the conversion. This is the default.
	LoadErrorAbort LoadErrorPolicy = "abort"
	// LoadErrorIgnore renders the error page Chromium produced.
	LoadErrorIgnore LoadErrorPolicy = "ignore"
	// LoadErrorSkip drops that input and continues with the remaining ones.
	LoadErrorSkip LoadErrorPolicy = "skip"
)

// WaitUntil is the navigation milestone to wait for before capturing.
type WaitUntil string

const (
	WaitLoad             WaitUntil = "load"
	WaitDOMContentLoaded WaitUntil = "domcontentloaded"
	WaitNetworkIdle      WaitUntil = "networkidle"
	// WaitNone captures as soon as navigation returns. The zero WaitUntil
	// waits for the load event instead.
	WaitNone WaitUntil = "none"
)

// Input is one HTML document. Set exactly one of URL, File, HTML, or Reader.
// Constructors such as FromURL enforce that.
type Input struct {
	// URL is an http(s) URL. file:// is accepted only when local file access
	// is enabled, and is served through the loopback file server.
	URL string
	// File is a local HTML file. Relative images and stylesheets next to the
	// file are served with it.
	File string
	// HTML is a raw document.
	HTML string
	// Reader is read to completion as a raw document. It is consumed once.
	Reader io.Reader
	// BaseDir is the directory used to resolve relative URLs in HTML or
	// Reader. It requires local file access.
	BaseDir string
	// Page, when set, replaces Request.Page for this input. Nil inherits.
	Page *Page
	// Header, when set, replaces Request.Header. A non-nil value with no
	// HTML, file, or text suppresses the header. Nil inherits. A cover
	// inherits neither header nor footer unless these fields are set.
	Header *HeaderFooter
	// Footer, when set, replaces Request.Footer. A non-nil empty value
	// suppresses the footer. Nil inherits.
	Footer *HeaderFooter
}

// FromURL returns an input that navigates to url.
func FromURL(url string) Input { return Input{URL: url} }

// FromFile returns an input that renders a local HTML file.
func FromFile(path string) Input { return Input{File: path} }

// FromHTML returns an input that renders a raw HTML document.
func FromHTML(html string) Input { return Input{HTML: html} }

// FromReader returns an input that reads a raw HTML document.
func FromReader(r io.Reader) Input { return Input{Reader: r} }

// Page is the PDF page box. The zero value is A4 portrait with 10 mm margins,
// a scale of 1, and print media. Background graphics default to on, which
// differs from wkhtmltopdf. Set PrintBackground to false to match it.
type Page struct {
	// Size is a named paper size. Empty means A4. Ignored when both Width and
	// Height are set.
	Size Paper
	// Width and Height override Size. Both must be set together.
	Width, Height Length
	Orientation   Orientation
	// Margin is the page margin. The zero Margin uses 10 mm on every side.
	// When a header or footer is set and this margin was not chosen explicitly
	// with Margins, the matching side is raised so the template is visible.
	Margin Margin
	// Scale is the print scale. 0 means 1. Chromium accepts 0.1 to 2.
	Scale float64
	// DPI is recorded for callers that mirror wkhtmltopdf. PDF output is
	// vector, and Chromium has no print-DPI parameter, so DPI does not resample
	// the PDF. Image requests use it as the device scale (DPI/96).
	DPI float64
	// PrintBackground prints CSS backgrounds. Nil means true.
	PrintBackground *bool
	// Media is print or screen. Empty means print.
	Media Media
	// SmartShrinking scales the page down so a layout wider than the paper
	// fits, which is the useful part of wkhtmltopdf --enable-smart-shrinking.
	// The library default is off. The CLI turns it on unless
	// --disable-smart-shrinking is passed.
	SmartShrinking bool
	// PreferCSSPageSize honors an @page size rule.
	PreferCSSPageSize bool
	// ViewportWidth and ViewportHeight set the layout viewport in CSS pixels
	// before printing. Zero leaves Chromium's print layout in charge.
	ViewportWidth  int
	ViewportHeight int
}

// Margin is a page margin. Use Margins or MarginAll so a deliberate zero is
// distinct from the default.
type Margin struct {
	Top, Right, Bottom, Left Length
	custom                   bool
}

// Margins returns a fully specified margin. A zero length on a side is a real
// zero, not the default.
func Margins(top, right, bottom, left Length) Margin {
	return Margin{Top: top, Right: right, Bottom: bottom, Left: left, custom: true}
}

// MarginAll returns the same length on every side.
func MarginAll(l Length) Margin { return Margins(l, l, l, l) }

// Custom reports whether the margin was set explicitly.
func (m Margin) Custom() bool { return m.custom }

// HeaderFooter is a running header or footer. HTML and File are Chromium
// header templates and may contain wkhtmltopdf tokens such as [page] and
// [topage]. Left, Center, and Right are used when HTML and File are empty.
// Replace supplies extra [token] values, including those set by wkhtmltopdf
// --replace.
type HeaderFooter struct {
	HTML     string
	File     string
	Left     string
	Center   string
	Right    string
	FontSize float64 // points, for Left/Center/Right
	FontName string
	Line     bool
	// Spacing is extra gap added to the matching page margin.
	Spacing Length
	Replace map[string]string
}

// Metadata is the PDF information dictionary. Empty fields are left alone.
type Metadata struct {
	Title    string
	Author   string
	Subject  string
	Keywords string
	Creator  string
	Producer string
}

// Outline controls the PDF bookmark tree generated from headings.
type Outline struct {
	// Enabled defaults to true when nil.
	Enabled *bool
	// Depth limits the heading levels copied into the outline. 0 means 6.
	Depth int
}

// TOC requests a table of contents built from headings. Page numbers come from
// the PDF outline Chromium writes, and each entry links to its heading.
type TOC struct {
	// Title defaults to "Table of Contents".
	Title string
	// Depth limits the heading levels listed. 0 means 3.
	Depth int
	// Template is a Go html/template for the contents page. Empty uses
	// DefaultTOCTemplate. The data value has Title and Entries. Each entry
	// has Level, Title, Page, Href, and Indent (pixels). This is the
	// replacement for wkhtmltopdf's XSLT stylesheet.
	Template string
	// Page, when set, is the page box of a separate contents document.
	// A single HTML input shares the body page unless Page is set, in which
	// case the contents are rendered on their own and merged in front.
	Page *Page
}

// Wait is how long to let the page settle before capturing.
type Wait struct {
	// Delay is a pause after the other conditions pass. wkhtmltopdf calls this
	// the JavaScript delay.
	Delay time.Duration
	// NetworkIdle waits until no resource has finished inside this window.
	// With WaitUntil set to WaitNetworkIdle and NetworkIdle 0, the window is
	// 500 ms.
	NetworkIdle time.Duration
	// Selector waits until an element matching this CSS selector exists.
	Selector string
	// WindowStatus waits until window.status equals this string, which is
	// wkhtmltopdf --window-status.
	WindowStatus string
	// Timeout bounds the whole render of one input. Zero uses the converter
	// default, or the context deadline when that is set.
	Timeout   time.Duration
	WaitUntil WaitUntil
}

// Network is the request environment for one conversion.
type Network struct {
	Headers  map[string]string
	Cookies  []Cookie
	Username string
	Password string
	// Proxy is an optional proxy URL for this request. A proxy that differs
	// from the converter's proxy starts a separate Chromium process.
	Proxy     string
	UserAgent string
	// AllowFileAccess permits local files and file:// URLs. Nil means allow
	// for inputs the caller passed as files. Set it to false to reject them.
	AllowFileAccess *bool
	// AllowPaths, when non-empty, restricts local files to these directories.
	AllowPaths []string
	// JavaScript enables page scripts. Nil means true. Injected scripts run
	// through the DevTools protocol either way.
	JavaScript *bool
	// Images enables image requests. Nil means true.
	Images *bool
	// BlockRemote fails requests whose host is not the document and not the
	// local machine. Set it for static HTML that must not contact the network.
	// Nil means allow.
	BlockRemote *bool
	// Timeout bounds navigation. Zero does not add a deadline of its own.
	Timeout time.Duration
}

// Cookie is an HTTP cookie set before navigation.
type Cookie struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	Secure   bool
	HTTPOnly bool
	SameSite string // "Lax", "Strict", or "None"
}

// Inject is CSS and JavaScript added to the page.
type Inject struct {
	CSS        string
	JavaScript string
	CSSFile    string
	JSFile     string
	// UserStyleSheet is an alias for a CSS file, matching wkhtmltopdf
	// --user-style-sheet. It is appended to CSSFile.
	UserStyleSheet string
}

// Request is a PDF conversion.
type Request struct {
	Inputs []Input
	// Cover is rendered without a header, footer, or table of contents and
	// placed first.
	Cover *Input
	TOC   *TOC

	Page     Page
	Header   *HeaderFooter
	Footer   *HeaderFooter
	Metadata Metadata
	Outline  Outline

	Wait    Wait
	Network Network
	Inject  Inject

	// Grayscale asks Chromium to paint the page in grayscale.
	Grayscale bool
	// PageRanges keeps a subset of the final document, for example "1-3,5".
	// Pages stay in document order.
	PageRanges string
	// PageOffset is added to displayed page numbers and to table-of-contents
	// numbers. It matches wkhtmltopdf --page-offset.
	PageOffset int
	// DisableExternalLinks strips http(s) hrefs before printing.
	DisableExternalLinks bool
	// DisableInternalLinks strips fragment hrefs before printing.
	DisableInternalLinks bool

	// LoadErrors defaults to LoadErrorAbort.
	LoadErrors LoadErrorPolicy
	// Copies repeats the finished document in the output file. 0 and 1 write
	// it once. This is wkhtmltopdf --copies: one file, not a printer queue.
	Copies int
	// Collate controls Copies. Nil means true: the whole document repeats.
	// False repeats each page Copies times before the next page.
	Collate *bool
	// MinimumFontSize raises computed font sizes below this floor, in points.
	// 0 leaves sizes alone. It is applied in the page before print, which is
	// the stand-in for WebKit's minimum-font-size setting.
	MinimumFontSize float64
	// Tagged keeps Chromium's accessible structure tree. The default is off.
	// Chrome writes a structure element for every marked node, and a large
	// table becomes many times larger than the same untagged PDF. Outlines,
	// link annotations, and extracted text do not need that tree. Set Tagged
	// when a consumer needs PDF/UA structure.
	Tagged bool
}

// ImageFormat is the encoding of a screenshot.
type ImageFormat string

const (
	PNG  ImageFormat = "png"
	JPEG ImageFormat = "jpeg"
	WEBP ImageFormat = "webp"
)

// Clip is a screenshot rectangle in CSS pixels.
type Clip struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// ImageRequest is a screenshot conversion.
type ImageRequest struct {
	Input  Input
	Format ImageFormat
	// Quality is the JPEG or WebP quality from 1 to 100. Zero means 90.
	// PNG ignores it.
	Quality int
	// Width is the layout width in CSS pixels. Zero means 1024.
	Width int
	// Height is the viewport height in CSS pixels. Zero with FullPage false
	// captures the full document height.
	Height int
	// FullPage captures past the viewport down the whole document. When Height
	// is zero this is implied.
	FullPage bool
	// Clip, when set, captures that rectangle instead of the viewport.
	Clip *Clip
	// Scale is the device scale factor. Zero means 1, or DPI/96 when DPI is set.
	Scale float64
	// DPI overrides Scale when Scale is zero: the device scale is DPI/96.
	DPI float64
	// OmitBackground makes the page background transparent. PNG and WebP only.
	OmitBackground bool
	Media          Media
	Grayscale      bool
	// SmartWidth grows the viewport to the document's scroll width. The CLI
	// turns this on to approximate wkhtmltoimage's smart width.
	SmartWidth bool

	Wait    Wait
	Network Network
	Inject  Inject
	// MinimumFontSize raises computed font sizes below this floor, in points.
	// 0 leaves sizes alone.
	MinimumFontSize float64
}

func boolOr(p *bool, fallback bool) bool {
	if p == nil {
		return fallback
	}
	return *p
}

func (o Outline) enabled() bool { return boolOr(o.Enabled, true) }

func (o Outline) depth() int {
	if o.Depth <= 0 {
		return 6
	}
	return o.Depth
}

func (t *TOC) title() string {
	if t == nil || t.Title == "" {
		return "Table of Contents"
	}
	return t.Title
}

func (t *TOC) depth() int {
	if t == nil || t.Depth <= 0 {
		return 3
	}
	return t.Depth
}
