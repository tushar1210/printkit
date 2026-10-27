// Command gen writes the local HTML fixtures used by the printkit benchmark.
// Run it from the repository root:
//
//	go run ./bench/gen
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := "bench/fixtures"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if err := os.MkdirAll(filepath.Join(root, "fonts"), 0o755); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "img"), 0o755); err != nil {
		fatal(err)
	}
	if err := copyFont(filepath.Join(root, "fonts", "LiberationSans-Regular.ttf")); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fonts", "NOTICE"), []byte(fontNotice), 0o644); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tiny.html"), []byte(tinyHTML), 0o644); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "report.html"), []byte(reportHTML()), 0o644); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "css.html"), []byte(cssHTML), 0o644); err != nil {
		fatal(err)
	}
	if err := writeImages(filepath.Join(root, "img")); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "images.html"), []byte(imagesHTML()), 0o644); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "table.html"), []byte(tableHTML(5000)), 0o644); err != nil {
		fatal(err)
	}
	fmt.Println("wrote fixtures under", root)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func copyFont(dst string) error {
	src := "/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf"
	b, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("copy font: %w", err)
	}
	return os.WriteFile(dst, b, 0o644)
}

const fontNotice = `Liberation Sans Regular is included so the CSS fixture can load a
font from disk. No network fetch is involved.

Copyright (c) 2012 Red Hat, Inc. with Reserved Font Name Liberation.
Digitized data copyright (c) 2010 Google Corporation.
Licensed under the SIL Open Font License, Version 1.1.
https://scripts.sil.org/OFL
`

const tinyHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Tiny</title>
</head>
<body>
<h1>Tiny marker</h1>
<p>TINYMARK. One short paragraph for a single page.</p>
</body>
</html>
`

func reportHTML() string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Warehouse audit</title>
<style>
  body { font-family: sans-serif; font-size: 11pt; line-height: 1.45; margin: 0; }
  article { page-break-after: always; break-after: page; }
  article:last-child { page-break-after: auto; break-after: auto; }
  h1 { font-size: 18pt; margin: 0 0 0.4em; }
  p { margin: 0 0 0.6em; }
</style>
</head>
<body>
`)
	titles := []string{
		"Receiving", "Putaway", "Cycle counts", "Cold storage", "Outbound",
		"Returns", "Packaging", "Labels", "Staffing", "Safety",
		"Carriers", "Damage", "Inventory", "Slots", "Audits",
	}
	for i := 1; i <= 48; i++ {
		title := titles[(i-1)%len(titles)]
		fmt.Fprintf(&b, "<article id=\"ch-%02d\">\n<h1>Chapter %02d. %s</h1>\n", i, i, title)
		fmt.Fprintf(&b, "<p>CHAPTERMARK-%02d. %s</p>\n", i, paragraph(i))
		fmt.Fprintf(&b, "<p>%s</p>\n<p>%s</p>\n</article>\n", paragraph(i+100), paragraph(i+200))
		b.WriteByte('\n')
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

func paragraph(seed int) string {
	bits := []string{
		"The night shift reconciled the aisle counts against the purchase orders before the dock doors opened.",
		"Two pallets of glass jars were quarantined after the wrap failed inspection at the inbound scale.",
		"Pick paths were rewritten so the fast movers sit on the first level and the slow movers move up.",
		"The supervisor logged each exception with the bin number, the expected quantity, and the name of the counter.",
		"A delayed carrier left twelve orders staged in the cage, and the labels were reprinted with the new departure time.",
		"Temperature probes in the cooler read half a degree high, so the set point was lowered and the log was signed.",
		"Returns from the west region were sorted into resale, refurbish, and scrap before noon.",
		"The audit sample covered every tenth location in aisles A through F and the whole of the cage.",
	}
	var b strings.Builder
	for n := 0; n < 6; n++ {
		if n > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(bits[(seed+n*3)%len(bits)])
	}
	return b.String()
}

const cssHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>CSS dashboard</title>
<style>
  @font-face {
    font-family: "Bench Sans";
    src: url("fonts/LiberationSans-Regular.ttf") format("truetype");
    font-weight: 400;
  }
  html, body { margin: 0; padding: 0; }
  body {
    font-family: "Bench Sans", sans-serif;
    color: #1c1917;
    background: linear-gradient(160deg, #fff7ed 0%, #e0f2fe 55%, #fce7f3 100%);
    min-height: 100vh;
  }
  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 28px 36px;
    background: rgba(255,255,255,0.8);
  }
  header h1 { margin: 0; font-size: 28px; letter-spacing: -0.03em; }
  header .meta { font-size: 13px; color: #57534e; }
  main {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 16px;
    padding: 28px 36px 48px;
  }
  .card {
    background: white;
    border-radius: 16px;
    padding: 18px 18px 22px;
    box-shadow: 0 10px 30px rgba(28, 25, 23, 0.08);
  }
  .card h2 { margin: 0 0 8px; font-size: 14px; color: #78716c; font-weight: 600; }
  .card .n { font-size: 32px; font-weight: 700; }
  .card:nth-child(1) { background: #ffedd5; }
  .card:nth-child(2) { background: #dbeafe; }
  .card:nth-child(3) { background: #dcfce7; }
  .card:nth-child(4) { background: #fae8ff; }
  .wide { grid-column: span 2; }
  .row { display: flex; gap: 8px; margin-top: 12px; }
  .pill {
    flex: 1;
    background: rgba(255,255,255,0.7);
    border-radius: 999px;
    padding: 6px 10px;
    font-size: 12px;
    text-align: center;
  }
</style>
</head>
<body>
<header>
  <h1>CSSMARK floor status</h1>
  <div class="meta">FONTMARK Bench Sans · local file, no network</div>
</header>
<main>
  <section class="card"><h2>Open orders</h2><div class="n">128</div><div class="row"><span class="pill">FLEXMARK pick</span><span class="pill">pack</span></div></section>
  <section class="card"><h2>Short picks</h2><div class="n">7</div></section>
  <section class="card"><h2>Dock doors</h2><div class="n">14</div></section>
  <section class="card"><h2>On time</h2><div class="n">96%</div></section>
  <section class="card wide"><h2>GRIDMARK aisle heat</h2><p>Four-column grid, rounded cards, a flex row of pills, a gradient page background, and a downloaded-from-disk face.</p></section>
  <section class="card"><h2>Trailers</h2><div class="n">5</div></section>
  <section class="card"><h2>Idle staff</h2><div class="n">3</div></section>
  <section class="card wide"><h2>Notes</h2><p>Modern layout is the point of this fixture. A browser from 2015 will not paint the grid or the flex line the way Chromium does.</p></section>
</main>
</body>
</html>
`

func writeImages(dir string) error {
	for i := 1; i <= 12; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 1024, 768))
		r := rand.New(rand.NewPCG(uint64(1000+i), 7))
		for y := 0; y < 768; y++ {
			for x := 0; x < 1024; x++ {
				img.SetRGBA(x, y, color.RGBA{
					R: uint8(r.Uint32()),
					G: uint8(r.Uint32() >> 8),
					B: uint8(40 + (i * 15)),
					A: 255,
				})
			}
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 55}); err != nil {
			return err
		}
		name := filepath.Join(dir, fmt.Sprintf("photo-%02d.jpg", i))
		if err := os.WriteFile(name, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func imagesHTML() string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Photo log</title>
<style>
  body { font-family: sans-serif; margin: 16px; }
  h1 { font-size: 20px; }
  .shot { page-break-inside: avoid; margin: 0 0 16px; }
  img { width: 640px; height: auto; }
  figcaption { font-size: 12px; }
</style>
</head>
<body>
<h1>IMAGEMARK photo log</h1>
`)
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "<figure class=\"shot\"><img src=\"img/photo-%02d.jpg\" alt=\"photo %d\"><figcaption>Photo %02d of the dock, shot locally.</figcaption></figure>\n", i, i, i)
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

func tableHTML(rows int) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>SKU table</title>
<style>
  body { font-family: sans-serif; font-size: 9pt; }
  table { border-collapse: collapse; width: 100%; }
  th, td { border: 1px solid #d6d3d1; padding: 2px 6px; text-align: left; }
  th { background: #f5f5f4; }
  tr { page-break-inside: avoid; }
</style>
</head>
<body>
<h1>TABLEMARK five thousand SKUs</h1>
<table>
<thead><tr><th>Row</th><th>SKU</th><th>Description</th><th>Qty</th><th>Bin</th></tr></thead>
<tbody>
`)
	for i := 1; i <= rows; i++ {
		fmt.Fprintf(&b, "<tr><td>ROW-%04d</td><td>SKU-%05d</td><td>Corrugated carton, burst strength 200, lot %d</td><td>%d</td><td>A-%02d-%02d</td></tr>\n",
			i, 10000+i, i%50, (i*7)%400, (i%40)+1, (i%20)+1)
	}
	b.WriteString("</tbody></table>\n</body></html>\n")
	return b.String()
}
