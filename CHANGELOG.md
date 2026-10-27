# Changelog

## v0.2.0 - 2026-10-28

- Default PDF is untagged. Chrome's structure tree was most of the warm time and the bytes of a large table (`printToPDF` about 2.0 s of 2.4 s, and 8,783,412 bytes before the tree was stripped). The outline is built from CSS page breaks and the printed content box, then written with pdfcpu. `Request.Tagged` / `--tagged-pdf` keeps Chrome's tree and its outline. Chrome's untagged `generateDocumentOutline` is not used: in Chrome 156 those destinations all point at the first page.
- `WritePDF` and `SavePDF` stream `Page.printToPDF` (`ReturnAsStream` and `IO.read`) when the request needs no outline, metadata, page ranges, copies, header, footer, cover, or merge. A single input with the default outline still comes back as bytes, because the outline is applied after print. pdfcpu is skipped when the request needs none of those passes. A structure tree is copied out only if `/StructTreeRoot` is actually present.
- `htmlprint serve` keeps one `Converter` on a unix socket (`PRINTKIT_SOCKET`, or `$TMPDIR/printkit-<uid>.sock`, mode 0600). `--listen` is a loopback TCP address. A later `htmlprint` uses it with `--use-daemon` or `PRINTKIT_DAEMON=1`, and starts Chrome itself when nothing is listening. A daemon that accepts the connection and then fails is not rendered a second time. wkhtmltopdf flags are unchanged.
- `FindChrome` prefers `chrome-headless-shell` when it is on `PATH` or in the usual install locations. `WithChromePath` and `CHROME_PATH` / `CHROMIUM_PATH` / `CHROME_BIN` still win. Launch turns off background networking, extensions, sync, component update, and the other idle Chrome work. On this machine a one-page PDF was 51 ms to launch and 79 ms end to end with the shell, and 159 ms / 232 ms with full Google Chrome.
- Memory knobs, all optional: `WithMaxJobsPerTab` (default 50), `WithRendererProcessLimit` (default the pool size; 0 leaves Chrome's limit), `WithJSHeapLimit` (default 256 MB; 0 removes the cap), and `WithMemoryLimit` (default 2 GiB of RSS for this profile; 0 disables the restart). A restart happens between jobs. `Converter.Metrics` reports tabs opened, tabs retired, and browser restarts.
- Readiness polls `document.readyState` (`WaitLoad` is the zero value, `WaitNone` opts out, `WaitNetworkIdle` and `Wait.WindowStatus` stay). `Network.BlockRemote` / `--block-remote` fails requests whose host is not the document and not the local machine. `Network.JavaScript` was already the `--disable-javascript` switch.
- A cover renders beside the body, and several inputs render together across the pool, then merge in order. A generated table of contents still waits for the body, because its page numbers come from that PDF.
- `WithTrace` or `PRINTKIT_TRACE=1` times launch, navigate, ready-wait, printToPDF, transfer, outline, pdfcpu post, and write. The bench accepts `-stages` and `-pprof`.

## v0.1.0

First release.

- Module path is `github.com/tushar1210/printkit` ([github.com/tushar1210/printkit](https://github.com/tushar1210/printkit)).
- Tagged PDF is opt-in (`Request.Tagged`, `--tagged-pdf`). The default prints Chrome's structure tree so the outline exists, then copies only the objects reachable from the catalog and the info dictionary. It does not follow `/SE`, `/StructTreeRoot`, or `/MarkInfo`. Bookmarks, table-of-contents links, and text extraction stay. The 5,000-row table went from 8,783,412 bytes to 473,001 bytes.
- Per-object page settings. `Input.Page`, `Input.Header`, and `Input.Footer` override the request. `TOC.Page` gives a separate contents document its own paper. The CLI applies page size, margins, zoom, background, media, smart shrinking, viewport, and header/footer flags to the `cover`, `toc`, or page they follow.
- `--copies` and `--collate` / `--no-collate`. Copies repeat the finished document in one file. Collation off repeats each page instead of the whole document.
- `--minimum-font-size` raises computed font sizes below a point size and leaves larger text alone.
- `--toc-style-sheet` is an HTML `html/template` (`TOC.Template`). `--dump-default-toc` prints the built-in template.
- Still rejected, with a reason: interactive forms (`Page.printToPDF` does not emit AcroForm fields), XSLT table-of-contents stylesheets, plugins, and the other flags listed in the README.
