# Changelog

## v0.1.0

First release.

- Module path is `github.com/tushar1210/printkit` ([github.com/tushar1210/printkit](https://github.com/tushar1210/printkit)).
- Tagged PDF is opt-in (`Request.Tagged`, `--tagged-pdf`). The default prints Chrome's structure tree so the outline exists, then copies only the objects reachable from the catalog and the info dictionary. It does not follow `/SE`, `/StructTreeRoot`, or `/MarkInfo`. Bookmarks, table-of-contents links, and text extraction stay. The 5,000-row table went from 8,783,412 bytes to 473,001 bytes.
- Per-object page settings. `Input.Page`, `Input.Header`, and `Input.Footer` override the request. `TOC.Page` gives a separate contents document its own paper. The CLI applies page size, margins, zoom, background, media, smart shrinking, viewport, and header/footer flags to the `cover`, `toc`, or page they follow.
- `--copies` and `--collate` / `--no-collate`. Copies repeat the finished document in one file. Collation off repeats each page instead of the whole document.
- `--minimum-font-size` raises computed font sizes below a point size and leaves larger text alone.
- `--toc-style-sheet` is an HTML `html/template` (`TOC.Template`). `--dump-default-toc` prints the built-in template.
- Still rejected, with a reason: interactive forms (`Page.printToPDF` does not emit AcroForm fields), XSLT table-of-contents stylesheets, plugins, and the other flags listed in the README.
