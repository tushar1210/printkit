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
- The import path is `github.com/tushar-singh-dev/printkit`.
- Google Chrome or Chromium on `PATH`, or `CHROME_PATH` / `WithChromePath`.
- Integration tests also use `pdftotext` and `pdfinfo` from poppler.

## Install

```bash
go get github.com/tushar-singh-dev/printkit@v0.1.0
```

The command:

```bash
go install github.com/tushar-singh-dev/printkit/cmd/htmlprint@v0.1.0
```

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

`--copies`, `--collate`, `--minimum-font-size`, `--enable-forms`, the checkbox and radio SVG flags, `--debug-javascript`, `--stop-slow-scripts`, `--no-stop-slow-scripts`, `--dump-outline`, `--read-args-from-stdin`, `--cache-dir`, `--enable-plugins`, `--disable-plugins`, `--keep-relative-links`, `--resolve-relative-links`, `--exclude-from-outline`, `--include-in-outline`, `--disable-dotted-lines`, `--disable-toc-back-links`, `--xsl-style-sheet`, `--dump-default-toc-xsl`.

Per-object options are not supported. wkhtmltopdf can give the cover different margins from the body. Here every flag applies to the whole conversion.

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
- Interactive form fields, copies, and an XSLT table of contents are not implemented.
- Header and footer HTML should set a font size. Chromium's default header text is tiny. The left/center/right fields use 9 pt unless `--header-font-size` says otherwise.
- `LoadErrorSkip` drops one input of a multi-input PDF. If every input is skipped, the call returns `ErrNoInput`.

## Performance vs wkhtmltopdf

Measured on 25 October 2026. Full tables, the parity notes, and the rerun command are in [BENCHMARKS.md](BENCHMARKS.md).

The machine was a KVM guest, Ubuntu 24.04.5, Linux 6.12.94+, 4 cores (Intel Xeon, 1 thread per core), 15.6 GiB RAM, no swap. printkit 0.1.0 on Go 1.26.0 drove Google Chrome 156.0.8078.47. wkhtmltopdf was 0.12.6.1 with patched Qt, the official jammy package `wkhtmltox_0.12.6.1-2.jammy_amd64.deb`. Ubuntu 24.04 has no official packaging build. Sixteen-wide runs were skipped. Eight-wide runs are oversubscribed on 4 cores.

Both CLIs ran with backgrounds on and smart shrinking left at the CLI default (on). wkhtmltopdf was passed `--enable-local-file-access`. The report adds a table of contents and a header/footer of `[page]/[topage]` and `[doctitle]`. Everything was local. Each series discarded 2 warm-up runs, then recorded 20 runs, or 8 batches for throughput. Percentiles are nearest-rank. CPU is the greater of `wait4` rusage and the high-water mark of user+sys time across the live process tree, so Chrome's renderer, GPU, and zygote count. RSS sums `VmRSS` and counts shared mappings once per process. PSS splits those pages. Prefer PSS when comparing memory cost.

Cold wall time is one process per document, which is what a shell script pays.

| Fixture | Tool | Wall p50 | Wall p95 | CPU p50 | RSS p50 | PSS p50 | Size |
| --- | --- | --- | --- | --- | --- | --- | --- |
| tiny | wkhtmltopdf | 222 ms | 224 ms | 21 ms | 34 MiB | 29 MiB | 10 KiB |
| tiny | htmlprint | 356 ms | 375 ms | 650 ms | 1.27 GiB | 392 MiB | 14 KiB |
| report | wkhtmltopdf | 718 ms | 724 ms | 117 ms | 44 MiB | 39 MiB | 176 KiB |
| report | htmlprint | 782 ms | 844 ms | 1.33 s | 1.43 GiB | 506 MiB | 235 KiB |
| css | wkhtmltopdf | 292 ms | 295 ms | 98 ms | 42 MiB | 37 MiB | 61 KiB |
| css | htmlprint | 404 ms | 432 ms | 820 ms | 1.37 GiB | 390 MiB | 123 KiB |
| images | wkhtmltopdf | 983 ms | 993 ms | 782 ms | 82 MiB | 77 MiB | 3.8 MiB |
| images | htmlprint | 462 ms | 493 ms | 950 ms | 1.37 GiB | 454 MiB | 3.8 MiB |
| table | wkhtmltopdf | 1.48 s | 1.65 s | 1.28 s | 82 MiB | 77 MiB | 655 KiB |
| table | htmlprint | 2.72 s | 2.84 s | 3.76 s | 1.99 GiB | 1.04 GiB | 8.4 MiB |

A warm `Converter` (pool size 1, browser already up) against the same wkhtmltopdf cold numbers:

| Fixture | Wall p50 | Wall p95 | CPU p50 | RSS p50 | PSS p50 | Size |
| --- | --- | --- | --- | --- | --- | --- |
| tiny | 91 ms | 105 ms | 280 ms | 1.39 GiB | 466 MiB | 14 KiB |
| report | 508 ms | 541 ms | 940 ms | 1.53 GiB | 588 MiB | 235 KiB |
| css | 142 ms | 151 ms | 420 ms | 1.47 GiB | 521 MiB | 123 KiB |
| images | 171 ms | 184 ms | 470 ms | 1.52 GiB | 601 MiB | 3.8 MiB |
| table | 2.37 s | 2.51 s | 3.52 s | 1.96 GiB | 1.13 GiB | 8.4 MiB |

Documents per second. `htmlprint` is N cold processes. `printkit` is one `Converter` with pool size N.

| Fixture | N | wkhtmltopdf | htmlprint | printkit |
| --- | --- | --- | --- | --- |
| tiny | 1 | 4.51 | 2.81 | 10.21 |
| tiny | 4 | 17.78 | 3.86 | 12.04 |
| tiny | 8 | 33.37 | 3.92 | 11.17 |
| report | 1 | 1.39 | 1.28 | 2.05 |
| report | 4 | 5.49 | 2.21 | 4.28 |
| report | 8 | 9.77 | 2.31 | 4.17 |
| css | 1 | 3.39 | 2.45 | 8.30 |
| css | 4 | 12.88 | 3.58 | 9.83 |
| css | 8 | 19.76 | 3.72 | 10.38 |
| images | 1 | 1.03 | 2.12 | 5.91 |
| images | 4 | 3.86 | 3.03 | 7.52 |
| images | 8 | 4.18 | 3.21 | 7.42 |
| table | 1 | 0.68 | 0.37 | 0.40 |
| table | 4 | 2.51 | 0.82 | 0.97 |
| table | 8 | 2.69 | 0.80 | 1.00 |

wkhtmltopdf wins the cold CLI comparison on the tiny page, the report, the CSS page, and the table. It uses less CPU on every fixture, and far less memory: about 30–80 MiB PSS for one process, against 390 MiB to 1.0 GiB for one Chrome. It also wins throughput anywhere several processes run at once. Eight wkhtmltopdf processes still fit. Eight htmlprint processes on the table peaked at 6.5 GiB PSS.

printkit wins after Chrome is already running, on every fixture except the table. Warm wall time is 91 ms, 508 ms, 142 ms, and 171 ms for tiny, report, css, and images. The photo log is faster even cold (462 ms vs 983 ms), and the warm pool stays ahead of wkhtmltopdf at 1, 4, and 8 workers (5.9, 7.5, and 7.4 docs/s vs 1.0, 3.9, and 4.2). Launching htmlprint once per document is the wrong way to scale it. The pool is the API to use.

The CSS fixture is not a win for wkhtmltopdf. Both embed Liberation Sans and both contain the marker text. wkhtmltopdf stacks the header, does not paint the four-column grid or the flex row, and leaves the page background white. printkit paints the gradient, the flex header, and the grid. The photo log and the 5,000-row table both contain all of their content. wkhtmltopdf uses 6 pages for the photos and 75 for the table. printkit uses 4 and 52.

Output size is close on the tiny page, the report, and the photos. The CSS page is 61 KiB vs 123 KiB. The table is 655 KiB vs 8.4 MiB. Chrome 156 writes a tagged structure tree, about 55,000 objects for this table. Turning `generateTaggedPDF` off in a one-off run cut that file to 413 KiB and the wall time to 1.66 s, and it also dropped the outline. Outlines stay on, so that switch was not shipped. Stripping the structure tree while keeping the outline is the cheap follow-up. It is not done here.
