package printkit

import (
	"strings"
	"testing"
	"time"
)

func TestApplyHeaderTemplate(t *testing.T) {
	when := time.Date(2026, 10, 25, 15, 4, 0, 0, time.UTC)
	static := ApplyHeaderTemplate(
		"[page]/[topage] [section] [title] [isodate] [time] [webpage] [frompage]",
		HeaderData{
			Page: 2, PageCount: 9, PageOffset: 3, Section: "Intro",
			Title: "A & B", URL: "https://example.test/a",
			Date: when, Static: true,
		},
	)
	want := "5/12 Intro A &amp; B 2026-10-25 15:04 https://example.test/a 4"
	if static != want {
		t.Fatalf("static:\n got %s\nwant %s", static, want)
	}
	native := ApplyHeaderTemplate("[page] of [topage] on [date]", HeaderData{Date: when})
	for _, piece := range []string{`class="pageNumber"`, `class="totalPages"`, `class="date"`} {
		if !strings.Contains(native, piece) {
			t.Fatalf("native %q missing %s", native, piece)
		}
	}
	replaced := ApplyHeaderTemplate("Hello [name]", HeaderData{
		Date: when, Replace: map[string]string{"name": "Ada"},
	})
	if replaced != "Hello Ada" {
		t.Fatalf("replace = %q", replaced)
	}
}

func TestHeaderSource(t *testing.T) {
	h := &HeaderFooter{Left: "[page]", Center: "Mid", Right: "End", FontSize: 12, Line: true}
	raw, err := h.render(HeaderData{Static: true, Page: 1, PageCount: 2, Date: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "font-size:12pt") || !strings.Contains(raw, ">1<") || !strings.Contains(raw, "border-bottom") {
		t.Fatalf("source = %s", raw)
	}
	if !templateNeedsStatic(&HeaderFooter{Left: "[section]"}, 0, false) {
		t.Fatal("section token should force the static header path")
	}
	if templateNeedsStatic(&HeaderFooter{Left: "[page]"}, 0, false) {
		t.Fatal("page token can stay native")
	}
	static, err := needsStatic(&HeaderFooter{Center: "[page]"}, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if !static {
		t.Fatal("page offset should force the static header path")
	}
}
