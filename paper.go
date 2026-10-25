package printkit

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Paper is a named page size. The zero value selects A4.
type Paper string

// Named page sizes. Dimensions follow ISO 216 for the A and B series and the
// common US sizes. Envelope names match wkhtmltopdf.
const (
	A0        Paper = "A0"
	A1        Paper = "A1"
	A2        Paper = "A2"
	A3        Paper = "A3"
	A4        Paper = "A4"
	A5        Paper = "A5"
	A6        Paper = "A6"
	A7        Paper = "A7"
	A8        Paper = "A8"
	A9        Paper = "A9"
	A10       Paper = "A10"
	B0        Paper = "B0"
	B1        Paper = "B1"
	B2        Paper = "B2"
	B3        Paper = "B3"
	B4        Paper = "B4"
	B5        Paper = "B5"
	B6        Paper = "B6"
	B7        Paper = "B7"
	B8        Paper = "B8"
	B9        Paper = "B9"
	B10       Paper = "B10"
	Letter    Paper = "Letter"
	Legal     Paper = "Legal"
	Tabloid   Paper = "Tabloid"
	Ledger    Paper = "Ledger"
	Executive Paper = "Executive"
	Folio     Paper = "Folio"
	C5E       Paper = "C5E"
	Comm10E   Paper = "Comm10E"
	DLE       Paper = "DLE"
)

// paperMM maps a lower-case paper name to width and height in millimeters.
var paperMM = map[string][2]float64{
	"a0": {841, 1189}, "a1": {594, 841}, "a2": {420, 594}, "a3": {297, 420},
	"a4": {210, 297}, "a5": {148, 210}, "a6": {105, 148}, "a7": {74, 105},
	"a8": {52, 74}, "a9": {37, 52}, "a10": {26, 37},
	"b0": {1000, 1414}, "b1": {707, 1000}, "b2": {500, 707}, "b3": {353, 500},
	"b4": {250, 353}, "b5": {176, 250}, "b6": {125, 176}, "b7": {88, 125},
	"b8": {62, 88}, "b9": {44, 62}, "b10": {31, 44},
	"letter": {215.9, 279.4}, "legal": {215.9, 355.6},
	"tabloid": {279.4, 431.8}, "ledger": {431.8, 279.4},
	"executive": {184.15, 266.7}, "folio": {215.9, 330.2},
	"c5e": {163, 229}, "comm10e": {105, 241}, "dle": {110, 220},
}

// PaperSize returns the portrait width and height of a named paper size.
// The name is case-insensitive. An empty name is A4.
func PaperSize(name Paper) (width, height Length, err error) {
	key := strings.ToLower(strings.TrimSpace(string(name)))
	if key == "" {
		key = "a4"
	}
	mm, ok := paperMM[key]
	if !ok {
		return Length{}, Length{}, optionf("Page.Size", "unknown paper size %q", name)
	}
	return MM(mm[0]), MM(mm[1]), nil
}

// KnownPapers returns the supported paper names in a stable order.
func KnownPapers() []Paper {
	return []Paper{
		A0, A1, A2, A3, A4, A5, A6, A7, A8, A9, A10,
		B0, B1, B2, B3, B4, B5, B6, B7, B8, B9, B10,
		Letter, Legal, Tabloid, Ledger, Executive, Folio, C5E, Comm10E, DLE,
	}
}

// Unit is the unit of a Length.
type Unit int

const (
	UnitMM Unit = iota
	UnitCM
	UnitIN
	UnitPX
	UnitPT
)

// Length is a CSS-style length. The zero value means "not set".
type Length struct {
	Value float64
	Unit  Unit
}

// MM returns a length in millimeters.
func MM(v float64) Length { return Length{Value: v, Unit: UnitMM} }

// CM returns a length in centimeters.
func CM(v float64) Length { return Length{Value: v, Unit: UnitCM} }

// IN returns a length in inches.
func IN(v float64) Length { return Length{Value: v, Unit: UnitIN} }

// PX returns a length in CSS pixels (1/96 inch).
func PX(v float64) Length { return Length{Value: v, Unit: UnitPX} }

// PT returns a length in points (1/72 inch).
func PT(v float64) Length { return Length{Value: v, Unit: UnitPT} }

// IsZero reports whether the length was left unset.
func (l Length) IsZero() bool { return l.Value == 0 }

// Inches converts the length to inches. CSS pixels use 96 dpi.
func (l Length) Inches() float64 {
	switch l.Unit {
	case UnitCM:
		return l.Value / 2.54
	case UnitIN:
		return l.Value
	case UnitPX:
		return l.Value / 96
	case UnitPT:
		return l.Value / 72
	default:
		return l.Value / 25.4
	}
}

// Millimeters converts the length to millimeters.
func (l Length) Millimeters() float64 { return l.Inches() * 25.4 }

// Points converts the length to PDF points.
func (l Length) Points() float64 { return l.Inches() * 72 }

// String renders the length with a unit suffix.
func (l Length) String() string {
	suffix := "mm"
	switch l.Unit {
	case UnitCM:
		suffix = "cm"
	case UnitIN:
		suffix = "in"
	case UnitPX:
		suffix = "px"
	case UnitPT:
		suffix = "pt"
	}
	return fmt.Sprintf("%g%s", l.Value, suffix)
}

// ParseLength parses values such as "210mm", "8.5in", "1cm", "72pt", "96px".
// A bare number is millimeters, matching wkhtmltopdf's page-width rule.
func ParseLength(s string) (Length, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return Length{}, optionf("length", "empty length")
	}
	unit := UnitMM
	for _, suf := range []struct {
		s string
		u Unit
	}{{"mm", UnitMM}, {"cm", UnitCM}, {"in", UnitIN}, {"px", UnitPX}, {"pt", UnitPT}} {
		if strings.HasSuffix(s, suf.s) {
			unit = suf.u
			s = strings.TrimSpace(strings.TrimSuffix(s, suf.s))
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return Length{}, optionf("length", "cannot parse %q", s)
	}
	return Length{Value: v, Unit: unit}, nil
}
