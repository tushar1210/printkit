# printkit

printkit renders HTML to PDF and to PNG, JPEG, or WebP. It is a Go module other projects import, plus a small `htmlprint` command that accepts the wkhtmltopdf and wkhtmltoimage flags scripts still use.

wkhtmltopdf is archived. It embeds Qt WebKit from 2015, so modern CSS and JavaScript come out wrong. printkit does not reimplement a browser. It drives a current Chromium and does the PDF work that is not printing in pure Go.

## Why this shape

| Piece | Choice | Why |
| --- | --- | --- |
| Browser | Headless Chromium | Page.printToPDF and Page.captureScreenshot already implement print layout, headers, and screenshots. A browser engine written in Go is not a serious option. |
| Protocol client | [chromedp](https://github.com/chromedp/chromedp) v0.21 | The call style is `context.Context` in and a value out, which matches the rest of the API. rod is a second framework with its own context story. A private CDP client would mean owning the target lifecycle and every protocol revision. |
| PDF post-processing | [pdfcpu](https://github.com/pdfcpu/pdfcpu) | Merge, outlines, the info dictionary, page selection, link annotations, and header stamps stay in Go. The module does not use cgo. |

Chromium prints one document at a time. Cover pages, several inputs, a generated table of contents, and continuous page numbers are assembled afterwards. Outlines come from Chromium's `generateDocumentOutline` (the heading tree with page numbers). A table of contents in a single HTML document is injected and linked with `#fragment` hrefs, which Chromium turns into annotations. Several inputs get a separate contents document, merged in front, with GoTo annotations.

Headers and footers use Chromium's header classes (`pageNumber`, `totalPages`, `date`) when the template only needs those. `[section]`, `[subsection]`, a page offset, or numbering that continues across merged inputs cannot be expressed that way. Those cases print the body with the margin reserved, render a one-line band per page, and stamp it on with pdfcpu.

## Requirements

- Go 1.26 or newer. chromedp v0.21 and pdfcpu v0.16 declare that as their minimum. The module follows them instead of pinning older releases to claim Go 1.22.
- Google Chrome or Chromium on `PATH`, or `CHROME_PATH` / `WithChromePath`.
- Integration tests also use `pdftotext` and `pdfinfo` from poppler.

## Install

The module path is `github.com/tushar1210/printkit`. The repository is private: [github.com/tushar1210/printkit](https://github.com/tushar1210/printkit).

```bash
go env -w GOPRIVATE=github.com/tushar1210
go get github.com/tushar1210/printkit@v0.1.0
go install github.com/tushar1210/printkit/cmd/htmlprint@v0.1.0
```

`GOPRIVATE` fetches that path directly and skips the public checksum database, so `GONOSUMDB` and `GONOPROXY` do not need a separate setting. Git has to present a credential that can read `tushar1210/printkit`: an HTTPS credential helper, or an SSH key registered on the GitHub account. Leave `GOPROXY` at its default.

From a checkout of this repository, `go build -o htmlprint ./cmd/htmlprint` builds the command without fetching the module.

A program named `wkhtmltopdf` or `wkhtmltoimage` selects PDF or image mode, so the binary can sit on `PATH` under those names.

## Library

```go
conv, err := printkit.New()
if err != nil {
    return err
}
defer conv.Close()

pdf, err := conv.PDF(ctx, printkit.Request{
    Inputs: []printkit.Input{printkit.FromHTML("<h1>Report</h1>")},
    Page:   printkit.Page{Size: printkit.A4, Margin: printkit.MarginAll(printkit.MM(12))},
    Header: &printkit.HeaderFooter{Center: "[page] / [topage]"},
    Metadata: printkit.Metadata{Title: "Report", Author: "Ada"},
})
```

`FromURL`, `FromFile`, and `FromReader` are the other inputs. `WritePDF` and `SavePDF` send the same request to an `io.Writer` or a path. `ConvertPDF` starts Chromium for one call. Images use `Image`, `WriteImage`, `SaveImage`, and `ConvertImage`.

`New` options are `WithChromePath`, `WithPoolSize` (default 4 tabs), `WithNoSandbox`, `WithChromeArgs`, `WithDefaultTimeout` (default 60s), `WithProxy`, `WithUserDataDir`, `WithEnv`, and `WithLogf`. A `Converter` is safe for concurrent use. `Close` stops Chromium and aborts work still running.

Local files are not opened as `file://` inside Chromium. They are served from `127.0.0.1` on an ephemeral port, with directory listings disabled and paths checked so a request cannot climb out of the mounted directory. `Network.AllowFileAccess` set to false rejects files. `AllowPaths` limits them to those directories.

Errors to match with `errors.Is`: `ErrChromeNotFound`, `ErrClosed`, `ErrNoInput`, `ErrFileAccessDenied`, `ErrInvalidOption` (`*OptionError`), `ErrLoadFailed` (`*LoadError`).

### Defaults

The zero `Request` prints A4 portrait, 10 mm margins, scale 1, CSS print media, and background graphics. Smart shrinking is off. Outlines from headings are on. JavaScript and images are on. When a header or footer is set and the margin was not chosen with `Margins` or `MarginAll`, that side is raised to 16 mm so the template is not clipped. An explicit zero margin stays zero.

Tagged PDF is off. Chrome 156 writes a structure element for every marked node, and it only emits `/Outlines` as part of that tree. Left in place, that tree was 8,783,412 bytes (8.4 MiB) and roughly 55,000 objects on the 5,000-row table. printkit still prints with the tree so the outline exists, then copies the objects reachable from the catalog and the info dictionary. It does not follow `/StructTreeRoot`, `/MarkInfo`, or an outline item's `/SE` link, and it drops those keys, so the structure elements are not written. Bookmarks, text extraction, and table-of-contents links stay. On 26 October 2026 that table was 473,001 bytes (462 KiB) with the outline intact. A PDF this rewriter cannot read falls back to a pdfcpu pass that frees the same objects. Set `Request.Tagged` or pass `--tagged-pdf` when a consumer needs the accessible structure tree. The default is off because wkhtmltopdf's output is untagged and the tree dominates file size on large tables. The outline is the feature scripts actually use.

`Page.DPI` does not resample a PDF. Print output is vector, and Chromium has no print-DPI switch. wkhtmltopdf's `--dpi` was already a no-op on X11. Image requests use DPI as the device scale (`DPI/96`) when `Scale` is zero.

## CLI

```bash
htmlprint page.html out.pdf
htmlprint --page-size Letter --header-center '[page]/[topage]' --footer-right '[section]' page.html out.pdf
htmlprint cover cover.html toc chapter.html out.pdf
htmlprint image --format png --width 800 page.html out.png
htmlprint pdf https://example.com - > example.pdf
```

`pdf` is the default mode. `image` is the other. The last positional is the output. `-` writes to standard output. `cover`, `toc`, and `page` are positional keywords, as in wkhtmltopdf.

CLI defaults follow wkhtmltopdf where that still matters, and they differ from the library:

| | Library | CLI |
| --- | --- | --- |
| CSS media | print | screen, unless `--print-media-type` |
| Background graphics | on | off, unless `--background` |
| Smart shrinking | off | on, unless `--disable-smart-shrinking` |
| Local files | allowed | allowed, unless `--disable-local-file-access` |
| Image smart width | off | on, unless `--disable-smart-width` |

wkhtmltopdf 0.12.6 turned local file access off by default. The CLI leaves it on so `htmlprint file.html out.pdf` works. Pass `--disable-local-file-access` to refuse files and `file://` URLs.

`--dpi` is accepted. It does not change a PDF. For images it is the device scale when `--zoom` is omitted.

`--lowquality` and `--custom-header-propagation` are accepted and do nothing. Headers set with `--custom-header` are sent on every request. PDF compression is left to Chromium.

### Flag map

| wkhtmltopdf / wkhtmltoimage | printkit |
| --- | --- |
| `cover`, `toc`, `page`, output `-` | same positionals |
| `--page-size`, `--page-width`, `--page-height`, `--orientation` | `Page.Size`, `Width`, `Height`, `Orientation` |
| `--margin-*` | `Page.Margin` via `Margins` |
| `--zoom` | `Page.Scale` or image `Scale` |
| `--grayscale` | `Request.Grayscale` / `ImageRequest.Grayscale` |
| `--print-media-type`, `--no-print-media-type` | `Page.Media` |
| `--background`, `--no-background` | `Page.PrintBackground` |
| `--disable-smart-shrinking`, `--enable-smart-shrinking` | `Page.SmartShrinking` |
| `--header-*`, `--footer-*`, `--replace` | `HeaderFooter`, including `[page]` `[topage]` `[date]` `[title]` `[section]` |
| `--title`, `--author`, `--subject`, `--keywords` | `Metadata` (`--author`, `--subject`, `--keywords` are extensions) |
| `--outline`, `--no-outline`, `--outline-depth` | `Outline` |
| `--toc`, `--toc-header-text`, `--cover` | `TOC`, `Cover` |
| `--toc-style-sheet <file>` | `TOC.Template`, an HTML `html/template`. `--dump-default-toc` prints the built-in one. |
| `--copies`, `--collate`, `--no-collate` | `Copies`, `Collate`. One file: the document repeated, or each page repeated. |
| `--minimum-font-size <pt>` | `MinimumFontSize`. Raises computed sizes below the floor. Does not shrink larger text. |
| `--tagged-pdf` | `Request.Tagged`. Keeps Chrome's structure tree. Off by default. |
| page flags after `cover`, `toc`, or a page | `Input.Page`, `Input.Header`, `Input.Footer`, and `TOC.Page`. A cover does not inherit the document header or footer. |
| `--page-offset` | `PageOffset` |
| `--disable-external-links`, `--disable-internal-links` | the same fields |
| `--javascript-delay` | `Wait.Delay` (milliseconds) |
| `--window-status` | `Wait.WindowStatus` |
| `--custom-header`, `--cookie` | `Network.Headers`, `Network.Cookies` |
| `--username`, `--password`, `--proxy`, `--user-agent` | `Network` |
| `--disable-javascript`, `--enable-javascript` | `Network.JavaScript` |
| `--images`, `--no-images` | `Network.Images` |
| `--enable-local-file-access`, `--disable-local-file-access`, `--allow` | `Network.AllowFileAccess`, `AllowPaths` |
| `--load-error-handling`, `--load-media-error-handling` | `LoadErrors` (`abort`, `ignore`, `skip`). Media errors use the same policy. |
| `--user-style-sheet`, `--run-script` | `Inject` |
| `--viewport-size` | `Page.ViewportWidth` and `ViewportHeight` |
| `--encoding utf-8` | accepted. Any other encoding is an error. |
| `--format`, `--quality`, `--width`, `--height`, `--crop-*` | `ImageRequest` |
| `--transparent` | `ImageRequest.OmitBackground` (PNG and WebP) |
| `--disable-smart-width`, `--enable-smart-width` | `ImageRequest.SmartWidth` |

Extensions that wkhtmltopdf did not have: `--wait-for` (CSS selector), `--wait-network-idle`, `--timeout`, `--chrome-path`, `--no-sandbox`, `--sandbox`, `--prefer-css-page-size`.

### Rejected flags

These fail with a reason instead of being ignored:

`--enable-forms`, the checkbox and radio SVG flags, `--debug-javascript`, `--stop-slow-scripts`, `--no-stop-slow-scripts`, `--dump-outline`, `--read-args-from-stdin`, `--cache-dir`, `--enable-plugins`, `--disable-plugins`, `--keep-relative-links`, `--resolve-relative-links`, `--exclude-from-outline`, `--include-in-outline`, `--disable-dotted-lines`, `--disable-toc-back-links`, `--xsl-style-sheet`, `--dump-default-toc-xsl`.

`--enable-forms` and the SVG widget flags stay rejected because Chromium's `Page.printToPDF` has no forms switch. It paints a static page and does not emit AcroForm fields. `--xsl-style-sheet` and `--dump-default-toc-xsl` stay rejected because the table of contents is HTML, not XSLT. Use `--toc-style-sheet` and `--dump-default-toc`.

Page size, margins, zoom, background, media, smart shrinking, viewport, and header/footer flags apply to the `cover`, `toc`, or page they follow. Flags before the first object are the defaults.

Exit status is 0 on success, 2 on a usage error, and 1 when the conversion fails.

## Containers

```bash
docker build -t htmlprint .
docker run --rm -v "$PWD":/work -w /work htmlprint page.html out.pdf
```

The image is Debian bookworm with Chromium, Liberation, and Noto, and it runs as uid 10001. `PRINTKIT_NO_SANDBOX=1` is set because many runtimes block user namespaces, which the Chromium sandbox needs. Unset it if your runtime allows the sandbox. Do not run the container as root unless you keep the sandbox off.

The library turns `--no-sandbox` on by itself when the process is root or `PRINTKIT_NO_SANDBOX` is `1`, `true`, or `yes`, unless `WithNoSandbox` was passed. It also passes `--disable-dev-shm-usage`. Give `/dev/shm` some room anyway if you clear that flag.

## Development

```bash
go test ./...
gofmt -l .
go vet ./...
golangci-lint run
```

Integration tests launch the Chrome found on the machine and read text back with poppler. Install both before `go test`.

```bash
# Debian/Ubuntu
sudo apt-get install -y poppler-utils
# Chrome: the google-chrome package, or chromium
```

GitHub Actions installs Chrome and poppler and runs format, vet, lint, and `go test ./...`.

## Known differences

- Rendering is Chromium, not Qt WebKit. Pages that depended on WebKit bugs will change. Pages that use current CSS will work.
- A PDF is vector. `--dpi` and `Page.DPI` do not resample it.
- Smart shrinking scales the page down only when the layout is wider than the printable width. It is not wkhtmltopdf's 1024 px viewport hack. The library default is off. The CLI default is on.
- `[section]` and `[subsection]` use the heading outline. A composed header is stamped in the margin. Native Chromium headers are used when the template does not need those tokens.
- A table of contents for one HTML input is part of that document and links to heading ids. A table of contents in front of several inputs uses estimated row positions for the click targets. The outline entries are exact.
- Interactive form fields are not implemented. `Page.printToPDF` has no AcroForm switch.
- `--copies` repeats pages inside one PDF. It does not talk to a printer.
- A custom table of contents is an HTML template (`TOC.Template`, `--toc-style-sheet`), not an XSLT stylesheet. The template data has `Title` and `Entries`, and each entry has `Level`, `Title`, `Page`, `Href`, and `Indent`.
- A single HTML file shares one page box with an injected table of contents. Set `TOC.Page` when the contents need their own paper size; printkit then renders them separately and merges them in front. Several inputs already do that. `Input.Page`, `Input.Header`, and `Input.Footer` override the request for that document. A nil header on a cover suppresses the document header.
- Header and footer HTML should set a font size. Chromium's default header text is tiny. The left/center/right fields use 9 pt unless `--header-font-size` says otherwise.
- `LoadErrorSkip` drops one input of a multi-input PDF. If every input is skipped, the call returns `ErrNoInput`.

## Performance vs wkhtmltopdf

Measured on 26 October 2026, after tagged PDF became opt-in. Full tables, the parity notes, and the rerun command are in [BENCHMARKS.md](BENCHMARKS.md). The 25 October run left Chrome's structure tree in the file; those table numbers are the before column in BENCHMARKS.md.

The machine was a KVM guest, Ubuntu 24.04.5, Linux 6.12.94+, 4 cores (Intel Xeon, 1 thread per core), 15.6 GiB RAM, no swap. printkit 0.1.0 on Go 1.26.0 drove Google Chrome 156.0.8078.47. wkhtmltopdf was 0.12.6.1 with patched Qt, the official jammy package `wkhtmltox_0.12.6.1-2.jammy_amd64.deb`. Ubuntu 24.04 has no official packaging build. Sixteen-wide runs were skipped. Eight-wide runs are oversubscribed on 4 cores.

Both CLIs ran with backgrounds on and smart shrinking left at the CLI default (on). wkhtmltopdf was passed `--enable-local-file-access`. The report adds a table of contents and a header/footer of `[page]/[topage]` and `[doctitle]`. Everything was local. Each series discarded 2 warm-up runs, then recorded 20 runs, or 8 batches for throughput. Percentiles are nearest-rank. CPU is the greater of `wait4` rusage and the high-water mark of user+sys time across the live process tree, so Chrome's renderer, GPU, and zygote count. RSS sums `VmRSS` and counts shared mappings once per process. PSS splits those pages. Prefer PSS when comparing memory cost.

Cold wall time is one process per document, which is what a shell script pays.

| Fixture | Tool | Wall p50 | Wall p95 | CPU p50 | RSS p50 | PSS p50 | Size |
| --- | --- | --- | --- | --- | --- | --- | --- |
| tiny | wkhtmltopdf | 225 ms | 228 ms | 25 ms | 34 MiB | 29 MiB | 10 KiB |
| tiny | htmlprint | 415 ms | 461 ms | 830 ms | 1.32 GiB | 364 MiB | 13 KiB |
| report | wkhtmltopdf | 724 ms | 756 ms | 122 ms | 44 MiB | 39 MiB | 176 KiB |
| report | htmlprint | 839 ms | 945 ms | 1.48 s | 1.43 GiB | 493 MiB | 225 KiB |
| css | wkhtmltopdf | 297 ms | 300 ms | 103 ms | 42 MiB | 37 MiB | 61 KiB |
| css | htmlprint | 497 ms | 543 ms | 990 ms | 1.37 GiB | 433 MiB | 118 KiB |
| images | wkhtmltopdf | 1.00 s | 1.06 s | 800 ms | 82 MiB | 76 MiB | 3.8 MiB |
| images | htmlprint | 531 ms | 581 ms | 1.06 s | 1.37 GiB | 457 MiB | 3.8 MiB |
| table | wkhtmltopdf | 1.51 s | 1.54 s | 1.31 s | 82 MiB | 77 MiB | 655 KiB |
| table | htmlprint | 2.99 s | 3.12 s | 4.17 s | 2.00 GiB | 1.04 GiB | 462 KiB |

A warm `Converter` (pool size 1, browser already up) against the same wkhtmltopdf cold numbers:

| Fixture | Wall p50 | Wall p95 | CPU p50 | RSS p50 | PSS p50 | Size |
| --- | --- | --- | --- | --- | --- | --- |
| tiny | 109 ms | 126 ms | 320 ms | 1.38 GiB | 469 MiB | 13 KiB |
| report | 513 ms | 543 ms | 970 ms | 1.53 GiB | 590 MiB | 225 KiB |
| css | 127 ms | 144 ms | 390 ms | 1.48 GiB | 518 MiB | 118 KiB |
| images | 176 ms | 187 ms | 460 ms | 1.52 GiB | 598 MiB | 3.8 MiB |
| table | 2.45 s | 2.64 s | 3.63 s | 1.95 GiB | 1.13 GiB | 462 KiB |

Documents per second. `htmlprint` is N cold processes. `printkit` is one `Converter` with pool size N.

| Fixture | N | wkhtmltopdf | htmlprint | printkit |
| --- | --- | --- | --- | --- |
| tiny | 1 | 4.49 | 2.47 | 9.73 |
| tiny | 4 | 17.69 | 3.38 | 10.39 |
| tiny | 8 | 33.34 | 3.90 | 11.73 |
| report | 1 | 1.39 | 1.34 | 2.18 |
| report | 4 | 5.42 | 2.26 | 4.39 |
| report | 8 | 9.77 | 2.29 | 4.46 |
| css | 1 | 3.43 | 2.47 | 7.34 |
| css | 4 | 13.31 | 3.42 | 9.36 |
| css | 8 | 19.72 | 3.52 | 9.57 |
| images | 1 | 1.02 | 2.03 | 5.68 |
| images | 4 | 3.80 | 2.86 | 7.01 |
| images | 8 | 4.12 | 3.00 | 7.09 |
| table | 1 | 0.67 | 0.37 | 0.41 |
| table | 4 | 2.46 | 0.80 | 0.97 |
| table | 8 | 2.62 | 0.78 | 1.01 |

wkhtmltopdf wins the cold CLI comparison on the tiny page, the report, the CSS page, and the table. It uses less CPU on every fixture, and far less memory: about 29–77 MiB PSS for one process, against 364 MiB to 1.04 GiB for one Chrome. It also wins throughput anywhere several processes run at once, except the photo log. Eight wkhtmltopdf processes still fit. Eight htmlprint processes on the table peaked at 6.45 GiB PSS.

printkit wins after Chrome is already running, on every fixture except the table. Warm wall time is 109 ms, 513 ms, 127 ms, and 176 ms for tiny, report, css, and images. The photo log is faster even cold (531 ms vs 1.00 s), and the warm pool stays ahead of wkhtmltopdf at 1, 4, and 8 workers (5.68, 7.01, and 7.09 docs/s vs 1.02, 3.80, and 4.12). Launching htmlprint once per document is the wrong way to scale it. The pool is the API to use.

The CSS fixture is not a win for wkhtmltopdf. Both embed Liberation Sans and both contain the marker text. wkhtmltopdf stacks the header, does not paint the four-column grid or the flex row, and leaves the page background white. printkit paints the gradient, the flex header, and the grid. The photo log and the 5,000-row table both contain all of their content. wkhtmltopdf uses 6 pages for the photos and 75 for the table. printkit uses 4 and 52.

Output size is close on the tiny page (10 KiB vs 13 KiB), the report (176 KiB vs 225 KiB), and the photos (both 3.8 MiB). The CSS page is 61 KiB vs 118 KiB. The table is where the structure tree used to dominate: on 25 October, with the tree left in the file, htmlprint wrote 8,783,412 bytes (8.4 MiB) against wkhtmltopdf's 670,285 bytes (655 KiB). This run writes 473,001 bytes (462 KiB) and keeps the outline. Wall time stayed near the previous run (cold 2.99 s, warm 2.45 s). Turning `generateTaggedPDF` off drops the outline as well, so that switch stays opt-in under `--tagged-pdf`. The default strips the tree after print.
