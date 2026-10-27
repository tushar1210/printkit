package printkit

import (
	"errors"
	"math"
	"testing"
)

func TestPaperSize(t *testing.T) {
	w, h, err := PaperSize(A4)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(w.Millimeters()-210) > 0.01 || math.Abs(h.Millimeters()-297) > 0.01 {
		t.Fatalf("A4 = %v x %v", w, h)
	}
	w, h, err = PaperSize("letter")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(w.Inches()-8.5) > 0.01 || math.Abs(h.Inches()-11) > 0.01 {
		t.Fatalf("Letter = %v x %v", w.Inches(), h.Inches())
	}
	if _, _, err := PaperSize("Poster"); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("unknown paper: %v", err)
	}
	if len(KnownPapers()) < 20 {
		t.Fatalf("known papers: %d", len(KnownPapers()))
	}
}

func TestParseLength(t *testing.T) {
	cases := []struct {
		in   string
		unit Unit
		mm   float64
	}{
		{"210", UnitMM, 210},
		{"210mm", UnitMM, 210},
		{"21cm", UnitCM, 210},
		{"1in", UnitIN, 25.4},
		{"96px", UnitPX, 25.4},
		{"72pt", UnitPT, 25.4},
		{" 8.5IN ", UnitIN, 8.5 * 25.4},
	}
	for _, tc := range cases {
		got, err := ParseLength(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if got.Unit != tc.unit {
			t.Fatalf("%s unit %v", tc.in, got.Unit)
		}
		if math.Abs(got.Millimeters()-tc.mm) > 0.02 {
			t.Fatalf("%s = %v mm, want %v", tc.in, got.Millimeters(), tc.mm)
		}
	}
	if _, err := ParseLength("nope"); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("junk: %v", err)
	}
	if got := MM(10).String(); got != "10mm" {
		t.Fatalf("String = %s", got)
	}
	if math.Abs(IN(1).Points()-72) > 0.001 {
		t.Fatal(IN(1).Points())
	}
}

func TestPageBox(t *testing.T) {
	box, err := (Page{Orientation: Landscape}).box(false, false)
	if err != nil {
		t.Fatal(err)
	}
	if box.width <= box.height {
		t.Fatalf("landscape box %#v", box)
	}
	box, err = (Page{Size: Letter}).box(true, true)
	if err != nil {
		t.Fatal(err)
	}
	if box.marginTop < MM(headerMarginMM).Inches()-0.01 {
		t.Fatalf("header margin not raised: %v", box.marginTop)
	}
	page := Page{Margin: MarginAll(MM(0))}
	box, err = page.box(true, true)
	if err != nil {
		t.Fatal(err)
	}
	if box.marginTop != 0 || !page.Margin.Custom() {
		t.Fatalf("explicit zero margin = %#v custom %v", box, page.Margin.Custom())
	}
	if _, err := (Page{Width: MM(10)}).box(false, false); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("half custom size: %v", err)
	}
	if _, err := (Page{Orientation: "sideways"}).box(false, false); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("orientation: %v", err)
	}
}

func TestFitScale(t *testing.T) {
	if got := fitScale(1, 2000, 1000, true); math.Abs(got-0.5) > 0.001 {
		t.Fatalf("shrink = %v", got)
	}
	if got := fitScale(1, 2000, 1000, false); got != 1 {
		t.Fatalf("no shrink = %v", got)
	}
	if got := fitScale(4, 100, 100, false); got != 2 {
		t.Fatalf("clamp = %v", got)
	}
	if got := clampScale(0); got != 1 {
		t.Fatalf("zero scale = %v", got)
	}
}
