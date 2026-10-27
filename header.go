package printkit

import (
	"fmt"
	"html"
	"os"
	"strconv"
	"strings"
	"time"
)

// HeaderData supplies values for wkhtmltopdf header and footer tokens.
type HeaderData struct {
	// Page and PageCount are 1-based. In native mode they are ignored for
	// [page] and [topage], which stay as Chromium fields so each page is
	// numbered by the browser. In static mode they are written literally.
	Page      int
	PageCount int
	// PageOffset is added to displayed page numbers. wkhtmltopdf --page-offset.
	PageOffset int
	Section    string
	Subsection string
	Title      string
	URL        string
	Date       time.Time
	Replace    map[string]string
	Static     bool
}

// ApplyHeaderTemplate rewrites wkhtmltopdf tokens in tmpl.
//
// Native mode (Static false) turns [page], [topage], [sitepage], [sitepages],
// and [date] into the Chromium classes pageNumber, totalPages, and date, so
// the browser fills them per page. [isodate], [time], [title], [doctitle],
// [webpage], [url], [section], and [subsection] are written immediately.
//
// Static mode writes every token immediately. The converter uses it when a
// template contains [section] or [subsection], when page numbers must stay
// continuous across merged inputs, or when a page offset is set.
func ApplyHeaderTemplate(tmpl string, data HeaderData) string {
	if data.Date.IsZero() {
		data.Date = time.Now()
	}
	replacer := make([]string, 0, 32)
	for k, v := range data.Replace {
		replacer = append(replacer, "["+k+"]", v)
	}
	page := data.Page + data.PageOffset
	from := 1 + data.PageOffset
	total := data.PageCount + data.PageOffset
	if total < from {
		total = from
	}
	if data.Static {
		replacer = append(replacer,
			"[page]", strconv.Itoa(page),
			"[sitepage]", strconv.Itoa(page),
			"[topage]", strconv.Itoa(total),
			"[sitepages]", strconv.Itoa(total),
			"[frompage]", strconv.Itoa(from),
			"[date]", data.Date.Format("2 Jan 2006"),
		)
	} else {
		replacer = append(replacer,
			"[page]", `<span class="pageNumber"></span>`,
			"[sitepage]", `<span class="pageNumber"></span>`,
			"[topage]", `<span class="totalPages"></span>`,
			"[sitepages]", `<span class="totalPages"></span>`,
			"[frompage]", strconv.Itoa(from),
			"[date]", `<span class="date"></span>`,
		)
	}
	replacer = append(replacer,
		"[isodate]", data.Date.Format("2006-01-02"),
		"[time]", data.Date.Format("15:04"),
		"[title]", html.EscapeString(data.Title),
		"[doctitle]", html.EscapeString(data.Title),
		"[webpage]", html.EscapeString(data.URL),
		"[url]", html.EscapeString(data.URL),
		"[section]", html.EscapeString(data.Section),
		"[subsection]", html.EscapeString(data.Subsection),
	)
	return strings.NewReplacer(replacer...).Replace(tmpl)
}

// templateNeedsStatic reports whether Chromium's header classes cannot express
// the template on their own.
func templateNeedsStatic(h *HeaderFooter, pageOffset int, continuous bool) bool {
	if h == nil {
		return false
	}
	if pageOffset != 0 || continuous {
		return true
	}
	raw := h.HTML + h.Left + h.Center + h.Right
	return strings.Contains(raw, "[section]") || strings.Contains(raw, "[subsection]")
}

func (h *HeaderFooter) source(lineAtTop bool) (string, error) {
	if h == nil {
		return "", nil
	}
	switch {
	case h.File != "":
		b, err := os.ReadFile(h.File)
		if err != nil {
			return "", fmt.Errorf("printkit: read header template: %w", err)
		}
		return string(b), nil
	case h.HTML != "":
		return h.HTML, nil
	case h.Left != "" || h.Center != "" || h.Right != "":
		size := h.FontSize
		if size <= 0 {
			size = 9
		}
		font := h.FontName
		if font == "" {
			font = "sans-serif"
		}
		border := ""
		if h.Line {
			if lineAtTop {
				border = "border-top:0.6px solid #444;"
			} else {
				border = "border-bottom:0.6px solid #444;"
			}
		}
		return fmt.Sprintf(
			`<div style="font-size:%gpt;font-family:%s;width:100%%;display:flex;justify-content:space-between;box-sizing:border-box;%s"><div>%s</div><div>%s</div><div>%s</div></div>`,
			size,
			html.EscapeString(font),
			border,
			html.EscapeString(h.Left),
			html.EscapeString(h.Center),
			html.EscapeString(h.Right),
		), nil
	default:
		return "", nil
	}
}

func (h *HeaderFooter) render(data HeaderData, lineAtTop bool) (string, error) {
	raw, err := h.source(lineAtTop)
	if err != nil || raw == "" {
		return raw, err
	}
	if h != nil && len(h.Replace) > 0 {
		merged := make(map[string]string, len(data.Replace)+len(h.Replace))
		for k, v := range data.Replace {
			merged[k] = v
		}
		for k, v := range h.Replace {
			merged[k] = v
		}
		data.Replace = merged
	}
	return ApplyHeaderTemplate(raw, data), nil
}

func headerActive(h *HeaderFooter) bool {
	if h == nil {
		return false
	}
	return h.HTML != "" || h.File != "" || h.Left != "" || h.Center != "" || h.Right != ""
}
