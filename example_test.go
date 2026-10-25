package printkit_test

import (
	"fmt"
	"time"

	"github.com/tushar-singh-dev/printkit"
)

func ExampleMM() {
	fmt.Println(printkit.MM(210))
	fmt.Printf("%.0f\n", printkit.IN(1).Points())
	// Output:
	// 210mm
	// 72
}

func ExampleParseLength() {
	n, err := printkit.ParseLength("8.5in")
	if err != nil {
		panic(err)
	}
	fmt.Printf("%.1fmm\n", n.Millimeters())
	// Output:
	// 215.9mm
}

func ExampleApplyHeaderTemplate() {
	when := time.Date(2026, 10, 25, 15, 4, 0, 0, time.UTC)
	fmt.Println(printkit.ApplyHeaderTemplate("[page] of [topage] — [section]", printkit.HeaderData{
		Page: 2, PageCount: 9, Section: "Intro", Date: when, Static: true,
	}))
	// Output:
	// 2 of 9 — Intro
}
