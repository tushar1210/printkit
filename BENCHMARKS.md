# printkit vs wkhtmltopdf

Numbers below are from one run of `./bench/run.sh` on 25 October 2026. Raw samples are in [bench/results.json](bench/results.json). The summary is also in the README.

## Machine

| | |
| --- | --- |
| OS | Ubuntu 24.04.5 LTS, Linux 6.12.94+, x86_64 |
| CPU | Intel Xeon (KVM), 4 cores, 1 thread per core, 1 socket |
| RAM | 15.6 GiB, no swap. About 4.5 GiB was available when the run started |
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
| tiny | wkhtmltopdf | 222 ms | 224 ms | 21 ms | 34 MiB | 29 MiB | 9,839 B |
| tiny | htmlprint | 356 ms | 375 ms | 650 ms | 1.27 GiB | 392 MiB | 14,546 B |
| report | wkhtmltopdf | 718 ms | 724 ms | 117 ms | 44 MiB | 39 MiB | 180,121 B |
| report | htmlprint | 782 ms | 844 ms | 1.33 s | 1.43 GiB | 506 MiB | 240,632 B |
| css | wkhtmltopdf | 292 ms | 295 ms | 98 ms | 42 MiB | 37 MiB | 62,386 B |
| css | htmlprint | 404 ms | 432 ms | 820 ms | 1.37 GiB | 390 MiB | 125,490 B |
| images | wkhtmltopdf | 983 ms | 993 ms | 782 ms | 82 MiB | 77 MiB | 3,948,007 B |
| images | htmlprint | 462 ms | 493 ms | 950 ms | 1.37 GiB | 454 MiB | 3,954,215 B |
| table | wkhtmltopdf | 1.48 s | 1.65 s | 1.28 s | 82 MiB | 77 MiB | 670,285 B |
| table | htmlprint | 2.72 s | 2.84 s | 3.76 s | 1.99 GiB | 1.04 GiB | 8,783,412 B |

## Warm library

One `Converter`, pool size 1. wkhtmltopdf's cold p50 is repeated so the comparison sits on one row. Sizes match the cold htmlprint files.

| Fixture | Wall p50 | Wall p95 | CPU p50 | RSS p50 | PSS p50 | wkhtml wall p50 |
| --- | --- | --- | --- | --- | --- | --- |
| tiny | 91 ms | 105 ms | 280 ms | 1.39 GiB | 466 MiB | 222 ms |
| report | 508 ms | 541 ms | 940 ms | 1.53 GiB | 588 MiB | 718 ms |
| css | 142 ms | 151 ms | 420 ms | 1.47 GiB | 521 MiB | 292 ms |
| images | 171 ms | 184 ms | 470 ms | 1.52 GiB | 601 MiB | 983 ms |
| table | 2.37 s | 2.51 s | 3.52 s | 1.96 GiB | 1.13 GiB | 1.48 s |

## Throughput

Documents per second, p50 then p95. CPU is per document. Memory is the peak of the whole batch.

| N | Fixture | Tool | Docs/s p50 | Docs/s p95 | CPU/doc p50 | PSS p50 | RSS p50 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | tiny | wkhtmltopdf | 4.51 | 4.53 | 21 ms | 29 MiB | 33 MiB |
| 1 | tiny | htmlprint | 2.81 | 3.00 | 650 ms | 392 MiB | 1.28 GiB |
| 1 | tiny | printkit | 10.21 | 11.14 | 300 ms | 435 MiB | 1.35 GiB |
| 4 | tiny | wkhtmltopdf | 17.78 | 17.89 | 21 ms | 48 MiB | 135 MiB |
| 4 | tiny | htmlprint | 3.86 | 4.07 | 680 ms | 925 MiB | 4.86 GiB |
| 4 | tiny | printkit | 12.04 | 13.58 | 268 ms | 635 MiB | 1.90 GiB |
| 8 | tiny | wkhtmltopdf | 33.37 | 33.73 | 20 ms | 72 MiB | 270 MiB |
| 8 | tiny | htmlprint | 3.92 | 4.17 | 675 ms | 1.25 GiB | 9.26 GiB |
| 8 | tiny | printkit | 11.17 | 12.12 | 295 ms | 892 MiB | 2.61 GiB |
| 1 | report | wkhtmltopdf | 1.39 | 1.40 | 119 ms | 39 MiB | 44 MiB |
| 1 | report | htmlprint | 1.28 | 1.30 | 1.34 s | 507 MiB | 1.44 GiB |
| 1 | report | printkit | 2.05 | 2.10 | 870 ms | 544 MiB | 1.49 GiB |
| 4 | report | wkhtmltopdf | 5.49 | 5.52 | 115 ms | 81 MiB | 178 MiB |
| 4 | report | htmlprint | 2.21 | 2.27 | 1.35 s | 1.28 GiB | 5.61 GiB |
| 4 | report | printkit | 4.28 | 4.38 | 845 ms | 950 MiB | 2.37 GiB |
| 8 | report | wkhtmltopdf | 9.77 | 10.02 | 115 ms | 134 MiB | 354 MiB |
| 8 | report | htmlprint | 2.31 | 2.36 | 1.37 s | 2.28 GiB | 11.11 GiB |
| 8 | report | printkit | 4.17 | 4.32 | 889 ms | 1.31 GiB | 3.41 GiB |
| 1 | css | wkhtmltopdf | 3.39 | 3.43 | 100 ms | 37 MiB | 42 MiB |
| 1 | css | htmlprint | 2.45 | 2.62 | 800 ms | 387 MiB | 1.38 GiB |
| 1 | css | printkit | 8.30 | 8.92 | 370 ms | 470 MiB | 1.42 GiB |
| 4 | css | wkhtmltopdf | 12.88 | 13.34 | 98 ms | 76 MiB | 168 MiB |
| 4 | css | htmlprint | 3.58 | 3.72 | 765 ms | 932 MiB | 5.15 GiB |
| 4 | css | printkit | 9.83 | 10.39 | 352 ms | 677 MiB | 2.09 GiB |
| 8 | css | wkhtmltopdf | 19.76 | 20.37 | 99 ms | 118 MiB | 337 MiB |
| 8 | css | htmlprint | 3.72 | 3.79 | 719 ms | 1.32 GiB | 9.21 GiB |
| 8 | css | printkit | 10.38 | 10.75 | 339 ms | 906 MiB | 2.97 GiB |
| 1 | images | wkhtmltopdf | 1.03 | 1.03 | 772 ms | 76 MiB | 82 MiB |
| 1 | images | htmlprint | 2.12 | 2.29 | 970 ms | 442 MiB | 1.37 GiB |
| 1 | images | printkit | 5.91 | 6.18 | 470 ms | 556 MiB | 1.47 GiB |
| 4 | images | wkhtmltopdf | 3.86 | 3.89 | 777 ms | 230 MiB | 322 MiB |
| 4 | images | htmlprint | 3.03 | 3.17 | 918 ms | 1.05 GiB | 5.31 GiB |
| 4 | images | printkit | 7.52 | 7.78 | 438 ms | 786 MiB | 2.22 GiB |
| 8 | images | wkhtmltopdf | 4.18 | 4.29 | 779 ms | 362 MiB | 574 MiB |
| 8 | images | htmlprint | 3.21 | 3.28 | 889 ms | 1.72 GiB | 9.58 GiB |
| 8 | images | printkit | 7.42 | 7.99 | 449 ms | 1.13 GiB | 3.05 GiB |
| 1 | table | wkhtmltopdf | 0.68 | 0.68 | 1.27 s | 77 MiB | 82 MiB |
| 1 | table | htmlprint | 0.37 | 0.39 | 3.70 s | 1.02 GiB | 1.99 GiB |
| 1 | table | printkit | 0.40 | 0.42 | 3.87 s | 1.11 GiB | 2.07 GiB |
| 4 | table | wkhtmltopdf | 2.51 | 2.58 | 1.28 s | 236 MiB | 326 MiB |
| 4 | table | htmlprint | 0.82 | 0.84 | 3.94 s | 3.42 GiB | 7.77 GiB |
| 4 | table | printkit | 0.97 | 1.02 | 3.67 s | 3.05 GiB | 4.42 GiB |
| 8 | table | wkhtmltopdf | 2.69 | 2.72 | 1.30 s | 446 MiB | 653 MiB |
| 8 | table | htmlprint | 0.80 | 0.83 | 4.14 s | 6.49 GiB | 15.43 GiB |
| 8 | table | printkit | 1.00 | 1.02 | 3.80 s | 5.47 GiB | 7.28 GiB |

## Conclusions

wkhtmltopdf is the faster and much smaller cold CLI, except on the photo log. One process is about 30–80 MiB PSS and a few hundred milliseconds for a simple page. Eight of them still scale: 33 tiny documents per second, 20 CSS pages per second. printkit pays Chrome startup on every cold process (about 360 ms and 390 MiB PSS for a one-line page, before the document itself gets expensive) and does not scale by launching more htmlprint processes. Eight cold htmlprint processes on the table peaked at 6.5 GiB PSS.

Keep a `Converter` and the picture changes for everything except the table. Warm wall time beats wkhtmltopdf on tiny (91 ms vs 222 ms), the report (508 ms vs 718 ms), CSS (142 ms vs 292 ms), and images (171 ms vs 983 ms). The warm pool is also the throughput winner on images at every concurrency, and on tiny, report, and CSS at one job. From 4 jobs up, wkhtmltopdf's small processes win those three again. The table stays with wkhtmltopdf at every setting: 1.48 s cold vs 2.37 s warm, and 2.69 docs/s vs 1.00 at 8-wide.

The CSS timing is not a quality win for wkhtmltopdf. The page it returns is missing the layout the fixture is there to test. The photo log and the table are complete in both tools, with different pagination.

File size is close on tiny, the report, and the photos. CSS is 62 KiB vs 125 KiB. The table is 670 KiB vs 8.4 MiB.

## Not changed

Chrome 156's print path writes a tagged PDF. The 5,000-row table came out as about 55,000 objects and 8.4 MiB. A one-off with `generateTaggedPDF` set false produced 412,511 bytes in 1.66 s wall (`/usr/bin/time`) and 52 pages, the same page count, and the outline was gone. Outlines are a supported feature, so the flag was not flipped. The follow-up that looks worth doing is to drop the structure tree after print and keep `/Outlines`. That rewrite was not attempted. Reusing tabs across documents was also left alone. A tab keeps the previous document's state.

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
