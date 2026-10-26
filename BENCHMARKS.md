# printkit vs wkhtmltopdf

Numbers below are from one run of `./bench/run.sh` on 26 October 2026. Tagged PDF was off, which is the default. Raw samples are in [bench/results.json](bench/results.json). The summary is also in the README. The 25 October run left Chrome's structure tree in the file; the table-size comparison is in the conclusions.

## Machine

| | |
| --- | --- |
| OS | Ubuntu 24.04.5 LTS, Linux 6.12.94+, x86_64 |
| CPU | Intel Xeon (KVM), 4 cores, 1 thread per core, 1 socket |
| RAM | 15.6 GiB, no swap. About 4.3 GiB was available when the run started |
| Go | go1.26.0 |
| printkit | 0.1.0 |
| Chrome | Google Chrome 156.0.8078.47 (`/usr/bin/google-chrome-stable`) |
| wkhtmltopdf | 0.12.6.1 (with patched Qt), `/usr/local/bin/wkhtmltopdf` |

The wkhtmltopdf binary is the official packaging build `wkhtmltox_0.12.6.1-2.jammy_amd64.deb` from [wkhtmltopdf/packaging](https://github.com/wkhtmltopdf/packaging/releases/download/0.12.6.1-2/wkhtmltox_0.12.6.1-2.jammy_amd64.deb). Ubuntu 24.04 has no official package. The jammy build is the closest patched-Qt release and it links against `libssl.so.3`. This is not the unpatched Ubuntu archive package.

Concurrency was 1, 4, and 8. Sixteen was skipped. This guest has 4 cores, so 8 is oversubscribed on purpose.

## Fixtures

`go run ./bench/gen` writes `bench/fixtures`. Nothing is fetched over the network.

| Fixture | What it is |
| --- | --- |
| tiny | One short paragraph (`TINYMARK`) |
| report | 48 chapters, each forced onto its own page, plus a generated table of contents. Header center `[page]/[topage]`, footer right `[doctitle]`. Those tokens stay on the native header path in both tools. `[section]` is not used |
| css | Flex header, four-column grid, gradient, `@font-face` from `fonts/LiberationSans-Regular.ttf` (SIL OFL 1.1, see `fonts/NOTICE`) |
| images | Twelve local 1024×768 JPEG photos, displayed at 640 px |
| table | 5,000 rows (`ROW-0001` through `ROW-5000`) |

## Method

Both CLIs get backgrounds on, so the CSS fixture is actually painted. Smart shrinking stays at each CLI's default, which is on for both. wkhtmltopdf is passed `--enable-local-file-access`. The printkit CLI already allows local files. The warm library request uses the same choices: screen media, smart shrinking, backgrounds on, and for the report a TOC plus the same header and footer.

| Scenario | What runs |
| --- | --- |
| Cold | One document per process. This is what a shell script pays. `htmlprint` against `wkhtmltopdf` |
| Warm | One `printkit.Converter` per fixture, pool size 1, timed around `Converter.PDF` after Chrome is already up. wkhtmltopdf has no warm mode |
| Throughput | 1, 4, and 8 documents at once. `wkhtmltopdf` and `htmlprint` are N processes. `printkit` is one `Converter` with pool size N |

Each cold and warm series discards 2 runs, then records 20. Throughput discards 1 batch, then records 8. Percentiles are nearest-rank: rank = ceil(p × n) − 1. For n = 20, p50 is the 10th sorted value and p95 is the 19th.

CPU for a finished command is the greater of two readings. `wait4` rusage includes children the process reaped, and it misses Chrome children that are not reaped into the parent. The live-tree sum is user+sys from `/proc/pid/stat` (`utime`, `stime`, `cutime`, `cstime`) across the process and its descendants, sampled every 15 ms, and it misses a few ticks that land as the process exits. Short wkhtmltopdf runs are in the second group. Chrome renders are in the first. Warm CPU is a delta of the live sum on the harness, so it includes a light sampler.

Peak RSS is the high-water mark of summed `VmRSS`. Shared pages are counted once per Chrome process, so RSS overstates the cost. PSS sums `Pss` from `smaps_rollup` about every 120 ms and splits those pages. Prefer PSS. `ru_maxrss` is not used.

## Parity

Markers were checked with `pdftotext`. Page counts with `pdfinfo`. Fonts with `pdffonts`. Images with `pdfimages -list`. Every marker was present. No run failed.

| Fixture | wkhtmltopdf | htmlprint |
| --- | --- | --- |
| tiny | 1 page, A4 | 1 page, A4 |
| report | 49 pages. TOC, header `1/49`, footer title | 49 pages. Same |
| css | 1 page. Liberation Sans embedded. 16 images in the PDF | 1 page. Liberation Sans embedded. 8 images in the PDF |
| images | 6 pages, 12 photos | 4 pages, 12 photos |
| table | 75 pages, `ROW-0001` and `ROW-5000` present | 52 pages, both rows present |

The CSS page is the one wkhtmltopdf renders wrong. Text and the web font are there. The layout is not: the header stacks, the four-column grid and the flex row collapse, and the gradient is missing (the page background is white). printkit paints the gradient, the flex header, and the grid. wkhtmltopdf also rasterizes more of that page, which is why `pdfimages` reports 16 images against 8.

The photo log is complete in both. wkhtmltopdf leaves more empty space and uses 6 pages. printkit fits two photos on a page and uses 4. The table contains all 5,000 rows in both. printkit fits more rows per page (52 pages vs 75).

## Cold CLI

| Fixture | Tool | Wall p50 | Wall p95 | CPU p50 | RSS p50 | PSS p50 | Size |
| --- | --- | --- | --- | --- | --- | --- | --- |
| tiny | wkhtmltopdf | 225 ms | 228 ms | 25 ms | 34 MiB | 29 MiB | 9,839 B |
| tiny | htmlprint | 415 ms | 461 ms | 830 ms | 1.32 GiB | 364 MiB | 13,822 B |
| report | wkhtmltopdf | 724 ms | 756 ms | 122 ms | 44 MiB | 39 MiB | 180,121 B |
| report | htmlprint | 839 ms | 945 ms | 1.48 s | 1.43 GiB | 493 MiB | 230,028 B |
| css | wkhtmltopdf | 297 ms | 300 ms | 103 ms | 42 MiB | 37 MiB | 62,386 B |
| css | htmlprint | 497 ms | 543 ms | 990 ms | 1.37 GiB | 433 MiB | 120,327 B |
| images | wkhtmltopdf | 1.00 s | 1.06 s | 800 ms | 82 MiB | 76 MiB | 3,948,007 B |
| images | htmlprint | 531 ms | 581 ms | 1.06 s | 1.37 GiB | 457 MiB | 3,948,626 B |
| table | wkhtmltopdf | 1.51 s | 1.54 s | 1.31 s | 82 MiB | 77 MiB | 670,285 B |
| table | htmlprint | 2.99 s | 3.12 s | 4.17 s | 2.00 GiB | 1.04 GiB | 473,001 B |

## Warm library

One `Converter`, pool size 1. wkhtmltopdf's cold p50 is repeated so the comparison sits on one row. Sizes match the cold htmlprint files.

| Fixture | Wall p50 | Wall p95 | CPU p50 | RSS p50 | PSS p50 | wkhtml wall p50 |
| --- | --- | --- | --- | --- | --- | --- |
| tiny | 109 ms | 126 ms | 320 ms | 1.38 GiB | 469 MiB | 225 ms |
| report | 513 ms | 543 ms | 970 ms | 1.53 GiB | 590 MiB | 724 ms |
| css | 127 ms | 144 ms | 390 ms | 1.48 GiB | 518 MiB | 297 ms |
| images | 176 ms | 187 ms | 460 ms | 1.52 GiB | 598 MiB | 1.00 s |
| table | 2.45 s | 2.64 s | 3.63 s | 1.95 GiB | 1.13 GiB | 1.51 s |

## Throughput

Documents per second, p50 then p95. CPU is per document. Memory is the peak of the whole batch.

| N | Fixture | Tool | Docs/s p50 | Docs/s p95 | CPU/doc p50 | PSS p50 | RSS p50 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | tiny | wkhtmltopdf | 4.49 | 4.52 | 22 ms | 29 MiB | 34 MiB |
| 1 | tiny | htmlprint | 2.47 | 2.55 | 780 ms | 368 MiB | 1.33 GiB |
| 1 | tiny | printkit | 9.73 | 11.03 | 310 ms | 437 MiB | 1.36 GiB |
| 4 | tiny | wkhtmltopdf | 17.69 | 17.84 | 22 ms | 49 MiB | 135 MiB |
| 4 | tiny | htmlprint | 3.38 | 3.98 | 772 ms | 888 MiB | 4.56 GiB |
| 4 | tiny | printkit | 10.39 | 12.51 | 298 ms | 631 MiB | 1.93 GiB |
| 8 | tiny | wkhtmltopdf | 33.34 | 33.80 | 21 ms | 72 MiB | 270 MiB |
| 8 | tiny | htmlprint | 3.90 | 4.04 | 692 ms | 1.14 GiB | 8.99 GiB |
| 8 | tiny | printkit | 11.73 | 12.75 | 286 ms | 793 MiB | 2.64 GiB |
| 1 | report | wkhtmltopdf | 1.39 | 1.40 | 116 ms | 39 MiB | 45 MiB |
| 1 | report | htmlprint | 1.34 | 1.38 | 1.30 s | 504 MiB | 1.43 GiB |
| 1 | report | printkit | 2.18 | 2.22 | 830 ms | 549 MiB | 1.48 GiB |
| 4 | report | wkhtmltopdf | 5.42 | 5.52 | 115 ms | 81 MiB | 178 MiB |
| 4 | report | htmlprint | 2.26 | 2.33 | 1.31 s | 1.26 GiB | 5.61 GiB |
| 4 | report | printkit | 4.39 | 4.56 | 820 ms | 944 MiB | 2.38 GiB |
| 8 | report | wkhtmltopdf | 9.77 | 9.90 | 116 ms | 134 MiB | 353 MiB |
| 8 | report | htmlprint | 2.29 | 2.37 | 1.37 s | 2.27 GiB | 11.13 GiB |
| 8 | report | printkit | 4.46 | 4.55 | 842 ms | 1.24 GiB | 3.43 GiB |
| 1 | css | wkhtmltopdf | 3.43 | 3.48 | 96 ms | 37 MiB | 42 MiB |
| 1 | css | htmlprint | 2.47 | 2.55 | 820 ms | 390 MiB | 1.37 GiB |
| 1 | css | printkit | 7.34 | 8.09 | 390 ms | 476 MiB | 1.44 GiB |
| 4 | css | wkhtmltopdf | 13.31 | 13.46 | 97 ms | 75 MiB | 168 MiB |
| 4 | css | htmlprint | 3.42 | 3.56 | 812 ms | 1016 MiB | 5.05 GiB |
| 4 | css | printkit | 9.36 | 10.33 | 365 ms | 658 MiB | 2.16 GiB |
| 8 | css | wkhtmltopdf | 19.72 | 20.19 | 100 ms | 117 MiB | 335 MiB |
| 8 | css | htmlprint | 3.52 | 3.61 | 805 ms | 1.48 GiB | 9.70 GiB |
| 8 | css | printkit | 9.57 | 10.07 | 364 ms | 882 MiB | 2.92 GiB |
| 1 | images | wkhtmltopdf | 1.02 | 1.02 | 781 ms | 77 MiB | 81 MiB |
| 1 | images | htmlprint | 2.03 | 2.17 | 1.00 s | 462 MiB | 1.38 GiB |
| 1 | images | printkit | 5.68 | 6.22 | 480 ms | 566 MiB | 1.47 GiB |
| 4 | images | wkhtmltopdf | 3.80 | 3.91 | 779 ms | 229 MiB | 323 MiB |
| 4 | images | htmlprint | 2.86 | 3.03 | 988 ms | 1.11 GiB | 5.38 GiB |
| 4 | images | printkit | 7.01 | 7.31 | 482 ms | 846 MiB | 2.22 GiB |
| 8 | images | wkhtmltopdf | 4.12 | 4.17 | 793 ms | 359 MiB | 575 MiB |
| 8 | images | htmlprint | 3.00 | 3.18 | 966 ms | 1.86 GiB | 10.37 GiB |
| 8 | images | printkit | 7.09 | 7.56 | 465 ms | 1.21 GiB | 3.17 GiB |
| 1 | table | wkhtmltopdf | 0.67 | 0.67 | 1.29 s | 77 MiB | 82 MiB |
| 1 | table | htmlprint | 0.37 | 0.39 | 3.67 s | 1.03 GiB | 1.99 GiB |
| 1 | table | printkit | 0.41 | 0.42 | 3.77 s | 1.13 GiB | 2.08 GiB |
| 4 | table | wkhtmltopdf | 2.46 | 2.54 | 1.30 s | 236 MiB | 326 MiB |
| 4 | table | htmlprint | 0.80 | 0.81 | 4.01 s | 3.37 GiB | 7.77 GiB |
| 4 | table | printkit | 0.97 | 0.98 | 3.68 s | 3.02 GiB | 4.42 GiB |
| 8 | table | wkhtmltopdf | 2.62 | 2.69 | 1.32 s | 446 MiB | 653 MiB |
| 8 | table | htmlprint | 0.78 | 0.82 | 4.27 s | 6.45 GiB | 15.48 GiB |
| 8 | table | printkit | 1.01 | 1.05 | 3.70 s | 5.40 GiB | 7.24 GiB |

## Conclusions

wkhtmltopdf is the faster and much smaller cold CLI, except on the photo log. One process is about 29–77 MiB PSS and a few hundred milliseconds for a simple page. Eight of them still scale: 33 tiny documents per second, 20 CSS pages per second. printkit pays Chrome startup on every cold process (415 ms and 364 MiB PSS for a one-line page) and does not scale by launching more htmlprint processes. Eight cold htmlprint processes on the table peaked at 6.45 GiB PSS.

Keep a `Converter` and the picture changes for everything except the table. Warm wall time beats wkhtmltopdf on tiny (109 ms vs 225 ms), the report (513 ms vs 724 ms), CSS (127 ms vs 297 ms), and images (176 ms vs 1.00 s). The warm pool is also the throughput winner on images at every concurrency, and on tiny, report, and CSS at one job. From 4 jobs up, wkhtmltopdf's small processes win those three again. The table stays with wkhtmltopdf on wall time at every setting: 1.51 s cold vs 2.45 s warm, and 2.62 docs/s vs 1.01 at 8-wide.

The CSS timing is not a quality win for wkhtmltopdf. The page it returns is missing the layout the fixture is there to test. The photo log and the table are complete in both tools, with different pagination.

File size is close on tiny (9,839 B vs 13,822 B), the report (180,121 B vs 230,028 B), and the photos (3,948,007 B vs 3,948,626 B). CSS is 62,386 B vs 120,327 B. The table is 670,285 B vs 473,001 B. On 25 October, before the structure tree was stripped, the same htmlprint table was 8,783,412 B. Outlines and the 52-page count stayed.

## Not changed

Reusing tabs across documents was left alone. A tab keeps the previous document's state. The structure-tree strip is in this run: tagged PDF is off unless `--tagged-pdf` / `Request.Tagged` is set, and the outline is kept. A one-off that only set `generateTaggedPDF` false was not shipped, because that also drops `/Outlines`.

## Rerun

From the repository root, with Chrome, wkhtmltopdf, and poppler (`pdfinfo`, `pdftotext`, `pdffonts`, `pdfimages`) installed:

```bash
./bench/run.sh
```

The script regenerates fixtures, rebuilds `bin/htmlprint` and `bin/printbench`, and writes `bench/results.json`. Scratch PDFs go to `bench/out/` and are not committed. Extra arguments are passed through:

```bash
./bench/run.sh -only tiny -runs 5 -warmup 1 -batches 2
```

Useful flags: `-runs` (default 20), `-warmup` (default 2), `-batches` (default 8), `-parallel` (default `1,4,8`), `-timeout` (default 3m), `-only`, `-skip-cold`, `-skip-warm`, `-skip-throughput`, `-skip-parity`.
