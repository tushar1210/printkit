# printkit

printkit renders HTML to PDF and to PNG, JPEG, or WebP. It is a Go module other projects import, plus a small `htmlprint` command that accepts the wkhtmltopdf and wkhtmltoimage flags scripts still use.

wkhtmltopdf is archived. It embeds Qt WebKit from 2015, so modern CSS and JavaScript come out wrong. printkit does not reimplement a browser. It drives a current Chromium and does the PDF work that is not printing in pure Go.

## Why this shape

| Piece | Choice | Why |
| --- | --- | --- |
| Browser | Headless Chromium | Page.printToPDF and Page.captureScreenshot already implement print layout, headers, and screenshots. A browser engine written in Go is not a serious option. |
| Protocol client | [chromedp](https://github.com/chromedp/chromedp) v0.21 | The call style is `context.Context` in and a value out, which matches the rest of the API. rod is a second framework with its own context story. A private CDP client would mean owning the target lifecycle and every protocol revision. |
| PDF post-processing | [pdfcpu](https://github.com/pdfcpu/pdfcpu) | Merge, outlines, the info dictionary, page selection, link annotations, and header stamps stay in Go. The module does not use cgo. |

A cover and the body can print at the same time, and several inputs render together across the tab pool, then merge in order. A generated table of contents waits for the body, because its page numbers come from that PDF. The default outline follows CSS page breaks and the printed content box, and is written with pdfcpu. `Request.Tagged` keeps Chromium's structure tree and `generateDocumentOutline`. A table of contents in a single HTML document is injected and linked with `#fragment` hrefs, which Chromium turns into annotations. Several inputs get a separate contents document, merged in front, with GoTo annotations.

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

`New` options are `WithChromePath`, `WithPoolSize` (default 4 tabs), `WithNoSandbox`, `WithChromeArgs`, `WithDefaultTimeout` (default 60s), `WithProxy`, `WithUserDataDir`, `WithEnv`, `WithLogf`, and `WithTrace`. Memory knobs, all optional: `WithMaxJobsPerTab` (default 50, then the tab is closed), `WithRendererProcessLimit` (default the pool size; 0 leaves Chrome's limit), `WithJSHeapLimit` (default 256 MB; 0 removes the cap), and `WithMemoryLimit` (default 2 GiB of RSS for this profile; 0 disables the restart). A restart happens between jobs, after the in-flight job finishes. `FindChrome` prefers `chrome-headless-shell` when it is on `PATH` or in the usual install locations. `WithChromePath` still wins, and `CHROME_PATH`, `CHROMIUM_PATH`, and `CHROME_BIN` still win over the search. A `Converter` is safe for concurrent use. `Close` stops Chromium and aborts work still running.

Local files are not opened as `file://` inside Chromium. They are served from `127.0.0.1` on an ephemeral port, with directory listings disabled and paths checked so a request cannot climb out of the mounted directory. `Network.AllowFileAccess` set to false rejects files. `AllowPaths` limits them to those directories.

Errors to match with `errors.Is`: `ErrChromeNotFound`, `ErrClosed`, `ErrNoInput`, `ErrFileAccessDenied`, `ErrInvalidOption` (`*OptionError`), `ErrLoadFailed` (`*LoadError`).

### Defaults

The zero `Request` prints A4 portrait, 10 mm margins, scale 1, CSS print media, and background graphics. Smart shrinking is off. Outlines from headings are on. JavaScript and images are on. When a header or footer is set and the margin was not chosen with `Margins` or `MarginAll`, that side is raised to 16 mm so the template is not clipped. An explicit zero margin stays zero.

Tagged PDF is off. Chrome 156 writes a structure element for every marked node, and it only emits `/Outlines` together with that tree. Generating the tree was most of the warm time of the 5,000-row table (about 2.0 s of 2.4 s) and, left in the file, 8,783,412 bytes. The default now prints with `generateTaggedPDF` false and builds the outline in Go, then writes it with pdfcpu. Page numbers follow CSS page breaks and the printed content box, so a chapter with `break-after: page` stays on its own outline page. Chrome's untagged `generateDocumentOutline` is not used: in Chrome 156 every one of those destinations points at the first page. Set `Request.Tagged` or pass `--tagged-pdf` when a consumer needs the accessible structure tree; that path keeps Chrome's tree and its outline. If an untagged PDF still contains `/StructTreeRoot`, printkit rewrites it with the fast copier that does not follow `/SE`, `/StructTreeRoot`, or `/MarkInfo`. A file that copier cannot read falls back to pdfcpu. A request with no outline, metadata, page ranges, copies, cover, or merge does not call pdfcpu at all, and `WritePDF` / `SavePDF` stream `Page.printToPDF` into the writer.

`Page.DPI` does not resample a PDF. Print output is vector, and Chromium has no print-DPI switch. wkhtmltopdf's `--dpi` was already a no-op on X11. Image requests use DPI as the device scale (`DPI/96`) when `Scale` is zero.

## CLI

```bash
htmlprint page.html out.pdf
htmlprint --page-size Letter --header-center '[page]/[topage]' --footer-right '[section]' page.html out.pdf
htmlprint cover cover.html toc chapter.html out.pdf
htmlprint image --format png --width 800 page.html out.png
htmlprint pdf https://example.com - > example.pdf
htmlprint serve
htmlprint --use-daemon page.html out.pdf
```

`htmlprint serve` keeps one Chromium process and answers on a unix socket (`PRINTKIT_SOCKET`, or `$TMPDIR/printkit-<uid>.sock`, mode 0600). `--listen` is a loopback TCP address instead. A later `htmlprint` uses that process only with `--use-daemon` or `PRINTKIT_DAEMON=1`. If nothing is listening, the command starts Chromium itself. A daemon that accepts the connection and then returns an error is not rendered a second time. The socket protocol is HTTP `POST /v1/render`. wkhtmltopdf flags are unchanged.

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
| `--block-remote` | `Network.BlockRemote`. Fails requests whose host is not the document and not the local machine. |
| `--use-daemon` | Use a running `htmlprint serve`. `PRINTKIT_DAEMON=1` does the same. |
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

Full tables, the per-stage profile, and the rerun command are in [BENCHMARKS.md](BENCHMARKS.md). Raw samples for the 28 October run are in [bench/results.json](bench/results.json).

The machine for both runs was a KVM guest, Ubuntu 24.04.5, Linux 6.12.94+, 4 cores (Intel Xeon, 1 thread per core), 15.6 GiB RAM, no swap. wkhtmltopdf was 0.12.6.1 with patched Qt, the official jammy package `wkhtmltox_0.12.6.1-2.jammy_amd64.deb`. Both CLIs ran with backgrounds on and smart shrinking at the CLI default (on). Each series discarded 2 warm-up runs, then recorded 20, or 8 batches for throughput. RSS sums `VmRSS`. PSS splits shared pages. Prefer RSS when a process exits in about one PSS sample (120 ms); the tiny cold PSS below is that case.

### Before, 26 October 2026

Google Chrome 156.0.8078.47, Go 1.26.0, printkit 0.1.0. The default still built a tagged PDF and then stripped the structure tree.

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

Warm `Converter`, pool size 1:

| Fixture | Wall p50 | Wall p95 | CPU p50 | RSS p50 | PSS p50 | Size |
| --- | --- | --- | --- | --- | --- | --- |
| tiny | 109 ms | 126 ms | 320 ms | 1.38 GiB | 469 MiB | 13 KiB |
| report | 513 ms | 543 ms | 970 ms | 1.53 GiB | 590 MiB | 225 KiB |
| css | 127 ms | 144 ms | 390 ms | 1.48 GiB | 518 MiB | 118 KiB |
| images | 176 ms | 187 ms | 460 ms | 1.52 GiB | 598 MiB | 3.8 MiB |
| table | 2.45 s | 2.64 s | 3.63 s | 1.95 GiB | 1.13 GiB | 462 KiB |

### After, 28 October 2026, 2:39 AM IST

Same guest. Go 1.26.10, printkit still reporting 0.1.0. `FindChrome` selected `chrome-headless-shell` 156.0.8078.47. A one-shot `New` plus a one-page PDF on this machine took 51 ms to launch and 79 ms end to end with the shell, and 159 ms to launch and 232 ms end to end with `/usr/local/bin/google-chrome`, both with `--no-sandbox` and pool size 1. The tables below are the 20-run series. Daemon mode is `htmlprint serve` with pool size 1; the wall time is the client, and RSS/PSS are the server process tree.

Cold:

| Fixture | Tool | Wall p50 | Wall p95 | CPU p50 | RSS p50 | PSS p50 | Size |
| --- | --- | --- | --- | --- | --- | --- | --- |
| tiny | wkhtmltopdf | 221 ms | 222 ms | 21 ms | 33 MiB | 27 MiB | 10 KiB |
| tiny | htmlprint | 120 ms | 129 ms | 100 ms | 521 MiB | 3 MiB | 12 KiB |
| report | wkhtmltopdf | 717 ms | 718 ms | 116 ms | 45 MiB | 37 MiB | 176 KiB |
| report | htmlprint | 436 ms | 458 ms | 460 ms | 598 MiB | 264 MiB | 226 KiB |
| css | wkhtmltopdf | 290 ms | 293 ms | 96 ms | 42 MiB | 36 MiB | 61 KiB |
| css | htmlprint | 154 ms | 162 ms | 160 ms | 553 MiB | 220 MiB | 114 KiB |
| images | wkhtmltopdf | 969 ms | 978 ms | 769 ms | 81 MiB | 71 MiB | 3.8 MiB |
| images | htmlprint | 191 ms | 199 ms | 230 ms | 578 MiB | 232 MiB | 3.8 MiB |
| table | wkhtmltopdf | 1.46 s | 1.48 s | 1.26 s | 82 MiB | 75 MiB | 655 KiB |
| table | htmlprint | 1.01 s | 1.04 s | 1.26 s | 952 MiB | 620 MiB | 389 KiB |

The tiny cold PSS of 3 MiB is the median of a process that exits in about 120 ms. Half of the `smaps_rollup` samples landed before the renderer mappings were charged. RSS for that row stays near 521 MiB on every sample.

Warm library, then daemon:

| Fixture | Warm p50 | Warm RSS | Warm PSS | Daemon p50 | Daemon RSS | Daemon PSS | Size |
| --- | --- | --- | --- | --- | --- | --- | --- |
| tiny | 16 ms | 477 MiB | 220 MiB | 18 ms | 478 MiB | 218 MiB | 12 KiB |
| report | 311 ms | 638 MiB | 365 MiB | 313 ms | 636 MiB | 356 MiB | 226 KiB |
| css | 37 ms | 645 MiB | 323 MiB | 41 ms | 644 MiB | 316 MiB | 114 KiB |
| images | 80 ms | 720 MiB | 416 MiB | 90 ms | 721 MiB | 419 MiB | 3.8 MiB |
| table | 895 ms | 1.08 GiB | 842 MiB | 921 ms | 1.09 GiB | 842 MiB | 389 KiB |

Documents per second, p50. `htmlprint` is N cold processes. `printkit` is one warm `Converter` with pool size N.

| Fixture | N | wkhtmltopdf | htmlprint | printkit |
| --- | --- | --- | --- | --- |
| tiny | 1 | 4.52 | 8.65 | 63.29 |
| tiny | 4 | 17.81 | 17.04 | 140.81 |
| tiny | 8 | 33.48 | 17.62 | 156.39 |
| report | 1 | 1.39 | 2.32 | 3.06 |
| report | 4 | 5.41 | 5.95 | 6.67 |
| report | 8 | 9.89 | 5.86 | 8.31 |
| css | 1 | 3.41 | 6.72 | 26.21 |
| css | 4 | 13.16 | 13.20 | 47.03 |
| css | 8 | 20.17 | 12.97 | 49.27 |
| images | 1 | 1.03 | 5.11 | 12.09 |
| images | 4 | 3.90 | 10.11 | 21.27 |
| images | 8 | 4.16 | 10.51 | 23.10 |
| table | 1 | 0.68 | 0.93 | 1.07 |
| table | 4 | 2.54 | 2.31 | 1.78 |
| table | 8 | 2.66 | 2.12 | 2.15 |

Cold htmlprint is now faster than wkhtmltopdf on every fixture. The table went from 2.99 s / 462 KiB to 1.01 s / 389 KiB because the default PDF is untagged and the outline follows CSS page breaks. Warm table time went from 2.45 s to 895 ms. RSS for one cold Chrome fell from 1.32–2.00 GiB to 521–952 MiB. wkhtmltopdf is still the small process: about 27–75 MiB PSS. A warm pool or `htmlprint serve` is the way to get the 16–90 ms times; a daemon round trip adds a few milliseconds on top of the warm library call. Eight cold htmlprint processes on the table still peaked at 7.12 GiB RSS. The CSS and photo layout notes from 26 October still hold: printkit paints the grid and fits the photos in 4 pages, wkhtmltopdf uses 6.
