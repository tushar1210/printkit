package printkit

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	cdptype "github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
)

// domBookmarks builds an outline from the layout. Chrome's untagged
// generateDocumentOutline writes a destination for every heading and points
// them all at the first page, and the tagged structure tree is what makes a
// large table slow. The script below repeats the breaks Chrome honours
// (break-before, break-after, and a content box that has filled up) against
// the printed content width, then the outline is written with pdfcpu.
func domBookmarks(ctx context.Context, box pageBox, scale float64, depth int) ([]pdfcpu.Bookmark, error) {
	if scale <= 0 {
		scale = 1
	}
	pageH := box.contentHeightPx() / scale
	width := int(math.Round(box.contentWidthPx() / scale))
	if pageH <= 0 || width < 1 {
		return nil, nil
	}
	// Measure at the width Chrome prints, not the tab's current window.
	// The override is cleared before the tab is reused.
	if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
		Width:             int64(width),
		Height:            800,
		DeviceScaleFactor: 1,
	}); err != nil {
		return nil, fmt.Errorf("printkit: outline: %w", err)
	}
	defer func() {
		_, _ = chromedp.Call(ctx, emulation.ClearDeviceMetricsOverride, cdptype.Empty{})
	}()

	script := strings.Replace(bookmarkJSONScript, "__PAGE_H__", strconv.FormatFloat(pageH, 'f', 3, 64), 1)
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[string](script))
	if err != nil {
		return nil, fmt.Errorf("printkit: outline: %w", err)
	}
	var heads []domHeading
	if err := json.Unmarshal([]byte(raw), &heads); err != nil {
		return nil, fmt.Errorf("printkit: outline: %w", err)
	}
	if depth <= 0 {
		depth = 6
	}
	var items []domMark
	for _, h := range heads {
		text := strings.TrimSpace(h.Text)
		if text == "" || h.Level < 1 || h.Level > depth {
			continue
		}
		page := h.Page
		if page < 1 {
			page = 1
		}
		items = append(items, domMark{level: h.Level, title: text, page: page})
	}
	return nestMarks(items), nil
}

type domHeading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	Page  int    `json:"page"`
}

type domMark struct {
	level int
	title string
	page  int
}

// bookmarkJSONScript returns heading text and the page it lands on.
// __PAGE_H__ is the printed content height in CSS pixels.
const bookmarkJSONScript = `(() => {
	const pageH = __PAGE_H__;
	const state = {page: 1, used: 0, out: []};
	const forces = (v) => v === 'page' || v === 'always' || v === 'left' || v === 'right';
	const breakBefore = (cs) => forces(cs.breakBefore) || forces(cs.pageBreakBefore);
	const breakAfter = (cs) => forces(cs.breakAfter) || forces(cs.pageBreakAfter);
	const outer = (el, cs) => {
		const r = el.getBoundingClientRect();
		return r.height + (parseFloat(cs.marginTop) || 0) + (parseFloat(cs.marginBottom) || 0);
	};
	const place = (h) => {
		if (!(h > 0)) return;
		if (h > pageH) {
			if (state.used > 0.5) {
				state.page++;
				state.used = 0;
			}
			const span = Math.ceil(h / pageH);
			state.page += span - 1;
			state.used = h - (span - 1) * pageH;
			return;
		}
		if (state.used > 0.5 && state.used + h > pageH + 0.5) {
			state.page++;
			state.used = h;
			return;
		}
		state.used += h;
	};
	const push = (el, page) => {
		const text = (el.innerText || '').trim();
		if (!text) return;
		state.out.push({level: Number(el.tagName.slice(1)), text: text, page: page});
	};
	const layout = (el) => {
		if (!el || el.nodeType !== 1) return;
		const cs = getComputedStyle(el);
		if (cs.display === 'none') return;
		if (cs.display === 'contents') {
			for (const k of el.children) layout(k);
			return;
		}
		if (breakBefore(cs) && state.used > 0.5) {
			state.page++;
			state.used = 0;
		}
		const disp = cs.display;
		// Flex and grid items compute as block, so walking them sums the same
		// row once per item. A table row is one fragment; styling every cell
		// is most of the cost on a large table. The container's border box is
		// the fragment height.
		const flexOrGrid = disp === 'flex' || disp === 'inline-flex' || disp === 'grid' || disp === 'inline-grid';
		const rowLike = disp === 'table-row' || flexOrGrid || cs.breakInside === 'avoid' || cs.pageBreakInside === 'avoid';
		if (rowLike) {
			const h = outer(el, cs);
			if (state.used > 0.5 && h > 0 && h <= pageH && state.used + h > pageH + 0.5) {
				state.page++;
				state.used = 0;
			}
			const page = state.page;
			place(h);
			if (disp === 'table-row') {
				for (const cell of el.children) {
					for (const kid of cell.children) {
						if (/^H[1-6]$/.test(kid.tagName)) push(kid, page);
					}
				}
			} else {
				el.querySelectorAll('h1,h2,h3,h4,h5,h6').forEach((hEl) => push(hEl, page));
			}
			if (breakAfter(cs)) {
				state.page++;
				state.used = 0;
			}
			return;
		}
		const flow = [];
		let inlineOnly = el.children.length > 0;
		for (const k of el.children) {
			const d = getComputedStyle(k).display;
			if (d === 'none') continue;
			flow.push(k);
			if (d === 'contents') continue;
			if (d !== 'inline' && d !== 'inline-block' && !String(d).startsWith('inline')) inlineOnly = false;
		}
		const heading = /^H[1-6]$/.test(el.tagName);
		const atomic = flow.length === 0 || inlineOnly;
		if (atomic) {
			const h = outer(el, cs);
			if (heading && state.used > 0.5 && h <= pageH && state.used + h > pageH + 0.5) {
				state.page++;
				state.used = 0;
			}
			const page = state.page;
			place(h);
			if (heading) push(el, page);
			else el.querySelectorAll('h1,h2,h3,h4,h5,h6').forEach((hEl) => push(hEl, page));
		} else {
			if (heading) push(el, state.page);
			for (const k of flow) layout(k);
		}
		if (breakAfter(cs)) {
			state.page++;
			state.used = 0;
		}
	};
	if (document.body) layout(document.body);
	return JSON.stringify(state.out);
})()`

type markNode struct {
	level int
	title string
	page  int
	kids  []*markNode
}

func nestMarks(items []domMark) []pdfcpu.Bookmark {
	var roots []*markNode
	var stack []*markNode
	for _, it := range items {
		n := &markNode{level: it.level, title: it.title, page: it.page}
		for len(stack) > 0 && stack[len(stack)-1].level >= it.level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, n)
		} else {
			parent := stack[len(stack)-1]
			parent.kids = append(parent.kids, n)
		}
		stack = append(stack, n)
	}
	out := make([]pdfcpu.Bookmark, len(roots))
	for i, n := range roots {
		out[i] = n.bookmark()
	}
	return out
}

func (n *markNode) bookmark() pdfcpu.Bookmark {
	bm := pdfcpu.Bookmark{Title: n.title, PageFrom: n.page}
	for _, k := range n.kids {
		bm.Kids = append(bm.Kids, k.bookmark())
	}
	return bm
}
