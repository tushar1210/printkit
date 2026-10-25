// Command printbench compares htmlprint and the printkit library with wkhtmltopdf.
//
// Cold runs are one document per process. Warm runs reuse one printkit
// Converter. Throughput runs 1, 4, and 8 documents at once. CPU for a finished
// command is the greater of wait4 rusage and the high-water mark of user+sys
// time across the live process tree. rusage misses Chromium children that are
// not reaped into the parent; the live sum misses ticks that land as the
// process exits. Peak RSS is the sum of VmRSS, sampled every 15ms, so shared
// pages are counted once per process. PSS sums smaps_rollup proportional set
// size and is sampled every eighth tick. ru_maxrss is not used.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/tushar-singh-dev/printkit"
)

const wkhtmlBuild = "wkhtmltox 1:0.12.6.1-2.jammy amd64 from https://github.com/wkhtmltopdf/packaging/releases/download/0.12.6.1-2/wkhtmltox_0.12.6.1-2.jammy_amd64.deb (patched Qt). Ubuntu 24.04 has no official packaging build; jammy is the closest and the package links against libssl.so.3."

const method = "Local fixtures only. Both CLIs run with backgrounds on and smart shrinking left at the CLI default (on). wkhtmltopdf is passed --enable-local-file-access. The report fixture adds a table of contents and a header/footer of [page]/[topage] and [doctitle], which both tools can paint with their native header path. Cold: one process per document. Warm: one printkit.Converter per fixture, pool size 1, timed around Converter.PDF after the browser is already up. Throughput: N processes, or one Converter with pool size N. CPU for a finished process is the greater of wait4 rusage and the high-water mark of user+sys ticks summed over the live process tree. rusage alone misses Chromium children that are not reaped into the parent. The live sum alone misses a few ticks that land in the parent as it exits, which is the whole cost of a short wkhtmltopdf run. Warm CPU is a delta of the live sum on the harness and includes a light RSS sampler. RSS sums VmRSS every 15ms and counts shared pages once per process. PSS sums smaps_rollup about every 120ms. Percentiles are nearest-rank, rank = ceil(p*n)-1."

func main() {
	htmlprint := flag.String("htmlprint", "bin/htmlprint", "htmlprint binary")
	wkhtml := flag.String("wkhtml", "wkhtmltopdf", "wkhtmltopdf binary")
	fixtureDir := flag.String("fixtures", "bench/fixtures", "fixture directory")
	outDir := flag.String("out", "bench/out", "scratch directory for PDFs")
	jsonPath := flag.String("json", "bench/results.json", "where to write the JSON report")
	runs := flag.Int("runs", 20, "measured runs per cold and warm series")
	warmup := flag.Int("warmup", 2, "discarded runs before each cold and warm series")
	batches := flag.Int("batches", 8, "measured throughput batches per concurrency level")
	parallel := flag.String("parallel", "1,4,8", "comma-separated concurrency levels")
	only := flag.String("only", "", "comma-separated fixture names to run")
	timeout := flag.Duration("timeout", 180*time.Second, "budget for one cold or warm conversion")
	skipCold := flag.Bool("skip-cold", false, "skip one-process-per-document runs")
	skipWarm := flag.Bool("skip-warm", false, "skip the warm printkit Converter runs")
	skipThroughput := flag.Bool("skip-throughput", false, "skip concurrency runs")
	skipParity := flag.Bool("skip-parity", false, "skip the output parity pass")
	flag.Parse()

	if *runs < 1 || *batches < 1 || *warmup < 0 {
		fatalf("runs and batches must be >= 1 and warmup must be >= 0")
	}
	levels, err := parseLevels(*parallel)
	if err != nil {
		fatalf("%v", err)
	}
	fixes, err := loadFixtures(*fixtureDir, *only)
	if err != nil {
		fatalf("%v", err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatalf("%v", err)
	}
	htmlPath, err := exec.LookPath(*htmlprint)
	if err != nil {
		htmlPath = *htmlprint
	}
	wkPath, err := exec.LookPath(*wkhtml)
	if err != nil {
		fatalf("wkhtmltopdf: %v", err)
	}

	rep := &report{
		MeasuredAt: time.Now().UTC(),
		Method:     method,
		Host:       collectHost(htmlPath, wkPath),
		Flags: runFlags{
			Runs: *runs, Warmup: *warmup, Batches: *batches,
			Parallel: levels, Timeout: timeout.String(),
		},
	}
	if err := writeReport(*jsonPath, rep); err != nil {
		fatalf("%v", err)
	}

	tools := []toolBin{
		{name: "wkhtmltopdf", path: wkPath, kind: "wkhtml"},
		{name: "htmlprint", path: htmlPath, kind: "htmlprint"},
	}

	if !*skipParity {
		for _, fix := range fixes {
			for _, tool := range tools {
				dst := filepath.Join(*outDir, "parity-"+tool.name+"-"+fix.name+".pdf")
				logf("parity %s %s", tool.name, fix.name)
				res := checkParity(tool, fix, dst, *timeout)
				rep.Parity = append(rep.Parity, res)
				if res.Error != "" {
					logf("  error: %s", res.Error)
				} else {
					logf("  pages=%d images=%d missing=%v notes=%v", res.Pages, res.Images, res.Missing, res.Notes)
				}
				if err := writeReport(*jsonPath, rep); err != nil {
					fatalf("%v", err)
				}
			}
		}
	}

	if !*skipCold {
		for _, fix := range fixes {
			for _, tool := range tools {
				logf("cold %s %s", tool.name, fix.name)
				s := measureCold(tool, fix, *outDir, *warmup, *runs, *timeout)
				rep.Cold = append(rep.Cold, s)
				logSeries(s)
				if err := writeReport(*jsonPath, rep); err != nil {
					fatalf("%v", err)
				}
			}
		}
	}

	if !*skipWarm {
		for _, fix := range fixes {
			logf("warm printkit %s", fix.name)
			s := measureWarm(fix, *warmup, *runs, *timeout)
			rep.Warm = append(rep.Warm, s)
			logSeries(s)
			if err := writeReport(*jsonPath, rep); err != nil {
				fatalf("%v", err)
			}
		}
	}

	if !*skipThroughput {
		batchBudget := *timeout * 2
		if batchBudget < 6*time.Minute {
			batchBudget = 6 * time.Minute
		}
		for _, fix := range fixes {
			for _, n := range levels {
				for _, tool := range tools {
					logf("throughput %s %s x%d", tool.name, fix.name, n)
					s := measureThroughput(tool, fix, *outDir, n, *batches, batchBudget)
					rep.Throughput = append(rep.Throughput, s)
					logThroughput(s)
					if err := writeReport(*jsonPath, rep); err != nil {
						fatalf("%v", err)
					}
				}
				logf("throughput printkit-warm %s x%d", fix.name, n)
				s := measureWarmThroughput(fix, n, *batches, batchBudget)
				rep.Throughput = append(rep.Throughput, s)
				logThroughput(s)
				if err := writeReport(*jsonPath, rep); err != nil {
					fatalf("%v", err)
				}
			}
		}
	}

	printReport(rep)
	if reportFailed(rep) {
		os.Exit(1)
	}
}

type toolBin struct {
	name string
	path string
	kind string
}

type fixture struct {
	name     string
	file     string
	toc      bool
	markers  []string
	minPages int
	maxPages int
}

func loadFixtures(dir, only string) ([]fixture, error) {
	want := map[string]bool{}
	if only != "" {
		for _, name := range strings.Split(only, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				want[name] = true
			}
		}
	}
	all := []fixture{
		{name: "tiny", file: "tiny.html", markers: []string{"TINYMARK"}, minPages: 1, maxPages: 2},
		{name: "report", file: "report.html", toc: true, markers: []string{"CHAPTERMARK-01", "CHAPTERMARK-48"}, minPages: 45},
		{name: "css", file: "css.html", markers: []string{"CSSMARK", "FONTMARK", "FLEXMARK", "GRIDMARK"}, minPages: 1, maxPages: 3},
		{name: "images", file: "images.html", markers: []string{"IMAGEMARK"}, minPages: 1},
		{name: "table", file: "table.html", markers: []string{"TABLEMARK", "ROW-0001", "ROW-5000"}, minPages: 40},
	}
	var out []fixture
	for _, fix := range all {
		if len(want) > 0 && !want[fix.name] {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(dir, fix.file))
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("fixture %s: %w", fix.name, err)
		}
		fix.file = abs
		out = append(out, fix)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no fixtures selected")
	}
	return out, nil
}

func parseLevels(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("parallel %q is not a positive integer", part)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no concurrency levels")
	}
	return out, nil
}

func cliArgs(kind string, fix fixture, out string, timeout time.Duration) []string {
	sec := int(timeout.Round(time.Second) / time.Second)
	if sec < 1 {
		sec = 1
	}
	var args []string
	if kind == "wkhtml" {
		args = []string{"--quiet", "--enable-local-file-access", "--background"}
	} else {
		args = []string{"--quiet", "--timeout", strconv.Itoa(sec), "--background"}
	}
	if fix.toc {
		args = append(args, "--header-center", "[page]/[topage]", "--footer-right", "[doctitle]", "toc")
	}
	return append(args, fix.file, out)
}

func libraryRequest(fix fixture) printkit.Request {
	bg := true
	req := printkit.Request{
		Inputs: []printkit.Input{printkit.FromFile(fix.file)},
		Page: printkit.Page{
			Media:           printkit.MediaScreen,
			SmartShrinking:  true,
			PrintBackground: &bg,
		},
	}
	if fix.toc {
		req.TOC = &printkit.TOC{}
		req.Header = &printkit.HeaderFooter{Center: "[page]/[topage]"}
		req.Footer = &printkit.HeaderFooter{Right: "[doctitle]"}
	}
	return req
}

type obs struct {
	wall time.Duration
	cpu  time.Duration
	rss  uint64
	pss  uint64
	size int64
}

func runExternal(ctx context.Context, bin string, args []string, outPath string) (obs, error) {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return obs{}, err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	configureCmd(cmd)
	stderr := &capped{max: 4096}
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return obs{}, err
	}
	var cpuPeak atomic.Int64
	var ticks atomic.Uint32
	stop := track(func() (uint64, uint64) {
		rss, pss, cpu := sampleTree(cmd.Process.Pid, ticks.Add(1)%8 == 1)
		raiseInt64(&cpuPeak, int64(cpu))
		return rss, pss
	})
	err := cmd.Wait()
	wall := time.Since(start)
	rss, pss := stop()
	cpu := time.Duration(cpuPeak.Load())
	if ru := processCPU(cmd.ProcessState); ru > cpu {
		cpu = ru
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return obs{}, fmt.Errorf("%s: %w", bin, err)
		}
		return obs{}, fmt.Errorf("%s: %w: %s", bin, err, msg)
	}
	st, err := os.Stat(outPath)
	if err != nil {
		return obs{}, err
	}
	if st.Size() == 0 {
		return obs{}, fmt.Errorf("%s: empty output %s", bin, outPath)
	}
	return obs{wall: wall, cpu: cpu, rss: rss, pss: pss, size: st.Size()}, nil
}

func measureCold(tool toolBin, fix fixture, outDir string, warmup, runs int, timeout time.Duration) series {
	var samples []sample
	var runErr error
	for i := 0; i < warmup+runs; i++ {
		out := filepath.Join(outDir, fmt.Sprintf("cold-%s-%s-%d.pdf", tool.name, fix.name, i))
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		got, err := runExternal(ctx, tool.path, cliArgs(tool.kind, fix, out, timeout), out)
		cancel()
		_ = os.Remove(out)
		if err != nil {
			runErr = err
			break
		}
		if i < warmup {
			continue
		}
		samples = append(samples, sampleFrom(got))
	}
	return summarize(tool.name, fix.name, samples, runErr)
}

func measureWarm(fix fixture, warmup, runs int, timeout time.Duration) series {
	conv, err := printkit.New(
		printkit.WithPoolSize(1),
		printkit.WithDefaultTimeout(timeout),
	)
	if err != nil {
		return summarize("printkit", fix.name, nil, err)
	}
	defer func() { _ = conv.Close() }()
	req := libraryRequest(fix)
	var samples []sample
	var runErr error
	for i := 0; i < warmup+runs; i++ {
		got, err := timeWarm(conv, req, timeout)
		if err != nil {
			runErr = err
			break
		}
		if i < warmup {
			continue
		}
		samples = append(samples, sampleFrom(got))
	}
	return summarize("printkit", fix.name, samples, runErr)
}

func timeWarm(conv *printkit.Converter, req printkit.Request, timeout time.Duration) (obs, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var ticks atomic.Uint32
	stop := track(func() (uint64, uint64) {
		rss, pss, _ := sampleTree(os.Getpid(), ticks.Add(1)%8 == 1)
		return rss, pss
	})
	cpu0 := treeCPU(os.Getpid())
	start := time.Now()
	pdf, err := conv.PDF(ctx, req)
	wall := time.Since(start)
	cpu1 := treeCPU(os.Getpid())
	rss, pss := stop()
	if err != nil {
		return obs{}, err
	}
	cpu := cpu1 - cpu0
	if cpu < 0 {
		cpu = 0
	}
	return obs{wall: wall, cpu: cpu, rss: rss, pss: pss, size: int64(len(pdf))}, nil
}

func measureThroughput(tool toolBin, fix fixture, outDir string, n, batches int, timeout time.Duration) throughputResult {
	var samples []batchSample
	var runErr error
	for i := 0; i < batches+1; i++ {
		tag := fmt.Sprintf("thr-%s-%s-x%d-%d", tool.name, fix.name, n, i)
		got, err := externalBatch(tool, fix, outDir, tag, n, timeout)
		if err != nil {
			runErr = err
			break
		}
		if i == 0 {
			continue
		}
		samples = append(samples, got)
	}
	return summarizeThroughput(tool.name, fix.name, n, samples, runErr)
}

func externalBatch(tool toolBin, fix fixture, outDir, tag string, n int, timeout time.Duration) (batchSample, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmds := make([]*exec.Cmd, n)
	outs := make([]string, n)
	for i := 0; i < n; i++ {
		outs[i] = filepath.Join(outDir, fmt.Sprintf("%s-%d.pdf", tag, i))
		cmds[i] = exec.CommandContext(ctx, tool.path, cliArgs(tool.kind, fix, outs[i], timeout)...)
		configureCmd(cmds[i])
		cmds[i].Stdout = io.Discard
		cmds[i].Stderr = io.Discard
	}
	started := 0
	start := time.Now()
	for i, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			for j := 0; j < started; j++ {
				_ = cmds[j].Process.Kill()
				_ = cmds[j].Wait()
			}
			return batchSample{}, err
		}
		started = i + 1
	}
	cpuPeak := make([]atomic.Int64, n)
	var ticks atomic.Uint32
	stop := track(func() (uint64, uint64) {
		wantPSS := ticks.Add(1)%8 == 1
		var rss, pss uint64
		for i, cmd := range cmds {
			if cmd.Process == nil {
				continue
			}
			r, p, cpu := sampleTree(cmd.Process.Pid, wantPSS)
			rss += r
			pss += p
			raiseInt64(&cpuPeak[i], int64(cpu))
		}
		return rss, pss
	})
	var size int64
	var first error
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil && first == nil {
			first = err
		}
		if st, err := os.Stat(outs[i]); err == nil {
			size += st.Size()
		}
		_ = os.Remove(outs[i])
	}
	wall := time.Since(start)
	rss, pss := stop()
	if first != nil {
		return batchSample{}, first
	}
	if wall <= 0 {
		return batchSample{}, fmt.Errorf("non-positive batch wall time")
	}
	var cpu time.Duration
	for i, cmd := range cmds {
		peak := time.Duration(cpuPeak[i].Load())
		if ru := processCPU(cmd.ProcessState); ru > peak {
			peak = ru
		}
		cpu += peak
	}
	return batchSample{
		sample: sample{
			WallNs:    wall.Nanoseconds(),
			CPUNs:     (cpu / time.Duration(n)).Nanoseconds(),
			RSSBytes:  rss,
			PSSBytes:  pss,
			SizeBytes: size / int64(n),
		},
		DocsPerSec: float64(n) / wall.Seconds(),
	}, nil
}

func measureWarmThroughput(fix fixture, n, batches int, timeout time.Duration) throughputResult {
	conv, err := printkit.New(
		printkit.WithPoolSize(n),
		printkit.WithDefaultTimeout(timeout),
	)
	if err != nil {
		return summarizeThroughput("printkit-warm", fix.name, n, nil, err)
	}
	defer func() { _ = conv.Close() }()
	req := libraryRequest(fix)
	var samples []batchSample
	var runErr error
	for i := 0; i < batches+1; i++ {
		got, err := warmBatch(conv, req, n, timeout)
		if err != nil {
			runErr = err
			break
		}
		if i == 0 {
			continue
		}
		samples = append(samples, got)
	}
	return summarizeThroughput("printkit-warm", fix.name, n, samples, runErr)
}

func warmBatch(conv *printkit.Converter, req printkit.Request, n int, timeout time.Duration) (batchSample, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var ticks atomic.Uint32
	stop := track(func() (uint64, uint64) {
		rss, pss, _ := sampleTree(os.Getpid(), ticks.Add(1)%8 == 1)
		return rss, pss
	})
	cpu0 := treeCPU(os.Getpid())
	start := time.Now()
	sizes := make([]int64, n)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pdf, err := conv.PDF(ctx, req)
			if err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
				return
			}
			sizes[i] = int64(len(pdf))
		}(i)
	}
	wg.Wait()
	wall := time.Since(start)
	cpu1 := treeCPU(os.Getpid())
	rss, pss := stop()
	if first != nil {
		return batchSample{}, first
	}
	if wall <= 0 {
		return batchSample{}, fmt.Errorf("non-positive batch wall time")
	}
	var size int64
	for _, sz := range sizes {
		size += sz
	}
	cpu := cpu1 - cpu0
	if cpu < 0 {
		cpu = 0
	}
	return batchSample{
		sample: sample{
			WallNs:    wall.Nanoseconds(),
			CPUNs:     (cpu / time.Duration(n)).Nanoseconds(),
			RSSBytes:  rss,
			PSSBytes:  pss,
			SizeBytes: size / int64(n),
		},
		DocsPerSec: float64(n) / wall.Seconds(),
	}, nil
}

func checkParity(tool toolBin, fix fixture, outPath string, timeout time.Duration) parityResult {
	res := parityResult{Tool: tool.name, Fixture: fix.name, PDF: outPath}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := runExternal(ctx, tool.path, cliArgs(tool.kind, fix, outPath, timeout), outPath); err != nil {
		res.Error = err.Error()
		return res
	}
	info, err := exec.Command("pdfinfo", outPath).Output()
	if err != nil {
		res.Error = fmt.Sprintf("pdfinfo: %v", err)
		return res
	}
	res.PageSize = infoField(string(info), "Page size")
	pages, err := strconv.Atoi(infoField(string(info), "Pages"))
	if err != nil {
		res.Error = fmt.Sprintf("pdfinfo pages: %v", err)
		return res
	}
	res.Pages = pages
	text, err := exec.Command("pdftotext", "-layout", outPath, "-").Output()
	if err != nil {
		res.Error = fmt.Sprintf("pdftotext: %v", err)
		return res
	}
	for _, marker := range fix.markers {
		if !bytes.Contains(text, []byte(marker)) {
			res.Missing = append(res.Missing, marker)
		}
	}
	if fix.name != "table" {
		res.TextHead = textHead(text, 400)
	}
	fonts, ferr := fontNames(outPath)
	if ferr != nil {
		res.Notes = append(res.Notes, "pdffonts: "+ferr.Error())
	} else {
		res.Fonts = fonts
	}
	images, ierr := imageCount(outPath)
	if ierr != nil {
		res.Notes = append(res.Notes, "pdfimages: "+ierr.Error())
	} else {
		res.Images = images
	}
	if pages < fix.minPages {
		res.Notes = append(res.Notes, fmt.Sprintf("page count %d is below %d", pages, fix.minPages))
	}
	if fix.maxPages > 0 && pages > fix.maxPages {
		res.Notes = append(res.Notes, fmt.Sprintf("page count %d is above %d", pages, fix.maxPages))
	}
	if fix.name == "images" && images < 12 {
		res.Notes = append(res.Notes, fmt.Sprintf("pdfimages found %d images, want at least 12", images))
	}
	if fix.name == "css" {
		joined := strings.ToLower(strings.Join(fonts, " "))
		if !strings.Contains(joined, "liberation") && !strings.Contains(joined, "bench") {
			res.Notes = append(res.Notes, "embedded fonts do not mention Liberation or Bench Sans")
		}
	}
	if len(res.Missing) > 0 {
		res.Notes = append(res.Notes, "missing text markers")
	}
	return res
}

func infoField(text, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func fontNames(path string) ([]string, error) {
	b, err := exec.Command("pdffonts", path).Output()
	if err != nil {
		return nil, err
	}
	var names []string
	past := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "---") {
			past = true
			continue
		}
		if !past {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		names = append(names, fields[0])
	}
	return names, nil
}

func imageCount(path string) (int, error) {
	b, err := exec.Command("pdfimages", "-list", path).Output()
	if err != nil {
		return 0, err
	}
	n := 0
	past := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "---") {
			past = true
			continue
		}
		if past && strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

func textHead(b []byte, n int) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > n {
		s = s[:n]
	}
	return s
}

type capped struct {
	b   bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if c.b.Len() < c.max {
		room := c.max - c.b.Len()
		if len(p) > room {
			_, _ = c.b.Write(p[:room])
		} else {
			_, _ = c.b.Write(p)
		}
	}
	return len(p), nil
}

func (c *capped) String() string { return c.b.String() }

func raiseInt64(dst *atomic.Int64, v int64) {
	for {
		old := dst.Load()
		if v <= old || dst.CompareAndSwap(old, v) {
			return
		}
	}
}

func track(read func() (uint64, uint64)) func() (uint64, uint64) {
	var peakR, peakP atomic.Uint64
	quit := make(chan struct{})
	done := make(chan struct{})
	raise := func(dst *atomic.Uint64, v uint64) {
		for {
			old := dst.Load()
			if v <= old || dst.CompareAndSwap(old, v) {
				return
			}
		}
	}
	go func() {
		defer close(done)
		tick := time.NewTicker(15 * time.Millisecond)
		defer tick.Stop()
		bump := func() {
			rss, pss := read()
			raise(&peakR, rss)
			raise(&peakP, pss)
		}
		bump()
		for {
			select {
			case <-quit:
				bump()
				return
			case <-tick.C:
				bump()
			}
		}
	}()
	return func() (uint64, uint64) {
		close(quit)
		<-done
		return peakR.Load(), peakP.Load()
	}
}

type report struct {
	MeasuredAt time.Time          `json:"measuredAt"`
	Method     string             `json:"method"`
	Host       hostInfo           `json:"host"`
	Flags      runFlags           `json:"flags"`
	Parity     []parityResult     `json:"parity"`
	Cold       []series           `json:"cold"`
	Warm       []series           `json:"warm"`
	Throughput []throughputResult `json:"throughput"`
}

type runFlags struct {
	Runs     int    `json:"runs"`
	Warmup   int    `json:"warmup"`
	Batches  int    `json:"batches"`
	Parallel []int  `json:"parallel"`
	Timeout  string `json:"timeout"`
}

type hostInfo struct {
	OS                string `json:"os"`
	Arch              string `json:"arch"`
	Uname             string `json:"uname"`
	CPUModel          string `json:"cpuModel"`
	CPUs              int    `json:"cpus"`
	MemTotalBytes     uint64 `json:"memTotalBytes"`
	MemAvailableBytes uint64 `json:"memAvailableBytes"`
	Go                string `json:"go"`
	Printkit          string `json:"printkit"`
	ChromePath        string `json:"chromePath"`
	ChromeVersion     string `json:"chromeVersion"`
	WkhtmlPath        string `json:"wkhtmlPath"`
	WkhtmlVersion     string `json:"wkhtmlVersion"`
	WkhtmlBuild       string `json:"wkhtmlBuild"`
	HTMLPrint         string `json:"htmlprint"`
}

type parityResult struct {
	Tool     string   `json:"tool"`
	Fixture  string   `json:"fixture"`
	PDF      string   `json:"pdf,omitempty"`
	Pages    int      `json:"pages,omitempty"`
	PageSize string   `json:"pageSize,omitempty"`
	Fonts    []string `json:"fonts,omitempty"`
	Images   int      `json:"images,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	Notes    []string `json:"notes,omitempty"`
	TextHead string   `json:"textHead,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type sample struct {
	WallNs    int64  `json:"wallNs"`
	CPUNs     int64  `json:"cpuNs"`
	RSSBytes  uint64 `json:"rssBytes"`
	PSSBytes  uint64 `json:"pssBytes"`
	SizeBytes int64  `json:"sizeBytes"`
}

type series struct {
	Tool         string   `json:"tool"`
	Fixture      string   `json:"fixture"`
	N            int      `json:"n"`
	WallP50Ms    float64  `json:"wallP50Ms"`
	WallP95Ms    float64  `json:"wallP95Ms"`
	CPUP50Ms     float64  `json:"cpuP50Ms"`
	CPUP95Ms     float64  `json:"cpuP95Ms"`
	RSSP50Bytes  uint64   `json:"rssP50Bytes"`
	RSSP95Bytes  uint64   `json:"rssP95Bytes"`
	PSSP50Bytes  uint64   `json:"pssP50Bytes"`
	PSSP95Bytes  uint64   `json:"pssP95Bytes"`
	SizeP50Bytes int64    `json:"sizeP50Bytes"`
	Error        string   `json:"error,omitempty"`
	Samples      []sample `json:"samples,omitempty"`
}

type batchSample struct {
	sample
	DocsPerSec float64 `json:"docsPerSec"`
}

type throughputResult struct {
	Tool          string        `json:"tool"`
	Fixture       string        `json:"fixture"`
	Parallel      int           `json:"parallel"`
	N             int           `json:"n"`
	DocsPerSecP50 float64       `json:"docsPerSecP50"`
	DocsPerSecP95 float64       `json:"docsPerSecP95"`
	WallP50Ms     float64       `json:"wallP50Ms"`
	WallP95Ms     float64       `json:"wallP95Ms"`
	CPUP50Ms      float64       `json:"cpuP50Ms"`
	CPUP95Ms      float64       `json:"cpuP95Ms"`
	RSSP50Bytes   uint64        `json:"rssP50Bytes"`
	RSSP95Bytes   uint64        `json:"rssP95Bytes"`
	PSSP50Bytes   uint64        `json:"pssP50Bytes"`
	PSSP95Bytes   uint64        `json:"pssP95Bytes"`
	SizeP50Bytes  int64         `json:"sizeP50Bytes"`
	Error         string        `json:"error,omitempty"`
	Samples       []batchSample `json:"samples,omitempty"`
}

func sampleFrom(o obs) sample {
	return sample{WallNs: o.wall.Nanoseconds(), CPUNs: o.cpu.Nanoseconds(), RSSBytes: o.rss, PSSBytes: o.pss, SizeBytes: o.size}
}

func summarize(tool, fix string, samples []sample, err error) series {
	s := series{Tool: tool, Fixture: fix, N: len(samples), Samples: samples}
	if err != nil {
		s.Error = err.Error()
	}
	if len(samples) == 0 {
		return s
	}
	walls := make([]float64, len(samples))
	cpus := make([]float64, len(samples))
	rss := make([]float64, len(samples))
	pss := make([]float64, len(samples))
	sizes := make([]float64, len(samples))
	for i, sm := range samples {
		walls[i] = float64(sm.WallNs) / 1e6
		cpus[i] = float64(sm.CPUNs) / 1e6
		rss[i] = float64(sm.RSSBytes)
		pss[i] = float64(sm.PSSBytes)
		sizes[i] = float64(sm.SizeBytes)
	}
	s.WallP50Ms, s.WallP95Ms = quantiles(walls)
	s.CPUP50Ms, s.CPUP95Ms = quantiles(cpus)
	r50, r95 := quantiles(rss)
	s.RSSP50Bytes, s.RSSP95Bytes = uint64(r50), uint64(r95)
	p50, p95 := quantiles(pss)
	s.PSSP50Bytes, s.PSSP95Bytes = uint64(p50), uint64(p95)
	z50, _ := quantiles(sizes)
	s.SizeP50Bytes = int64(z50)
	return s
}

func summarizeThroughput(tool, fix string, parallel int, samples []batchSample, err error) throughputResult {
	s := throughputResult{Tool: tool, Fixture: fix, Parallel: parallel, N: len(samples), Samples: samples}
	if err != nil {
		s.Error = err.Error()
	}
	if len(samples) == 0 {
		return s
	}
	walls := make([]float64, len(samples))
	cpus := make([]float64, len(samples))
	rss := make([]float64, len(samples))
	pss := make([]float64, len(samples))
	sizes := make([]float64, len(samples))
	docs := make([]float64, len(samples))
	for i, sm := range samples {
		walls[i] = float64(sm.WallNs) / 1e6
		cpus[i] = float64(sm.CPUNs) / 1e6
		rss[i] = float64(sm.RSSBytes)
		pss[i] = float64(sm.PSSBytes)
		sizes[i] = float64(sm.SizeBytes)
		docs[i] = sm.DocsPerSec
	}
	s.WallP50Ms, s.WallP95Ms = quantiles(walls)
	s.CPUP50Ms, s.CPUP95Ms = quantiles(cpus)
	r50, r95 := quantiles(rss)
	s.RSSP50Bytes, s.RSSP95Bytes = uint64(r50), uint64(r95)
	p50, p95 := quantiles(pss)
	s.PSSP50Bytes, s.PSSP95Bytes = uint64(p50), uint64(p95)
	z50, _ := quantiles(sizes)
	s.SizeP50Bytes = int64(z50)
	s.DocsPerSecP50, s.DocsPerSecP95 = quantiles(docs)
	return s
}

func quantiles(vals []float64) (p50, p95 float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	return nearestRank(s, 0.50), nearestRank(s, 0.95)
}

func nearestRank(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func collectHost(htmlprint, wkhtml string) hostInfo {
	h := hostInfo{
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		CPUs:        runtime.NumCPU(),
		Go:          runtime.Version(),
		Printkit:    printkit.Version,
		HTMLPrint:   htmlprint,
		WkhtmlPath:  wkhtml,
		WkhtmlBuild: wkhtmlBuild,
	}
	if b, err := exec.Command("uname", "-srm").Output(); err == nil {
		h.Uname = strings.TrimSpace(string(b))
	}
	h.CPUModel = cpuModel()
	h.MemTotalBytes, h.MemAvailableBytes = memInfo()
	if chrome, err := printkit.FindChrome(); err == nil {
		h.ChromePath = chrome
		if b, err := exec.Command(chrome, "--version").Output(); err == nil {
			h.ChromeVersion = strings.TrimSpace(string(b))
		}
	}
	if b, err := exec.Command(wkhtml, "--version").CombinedOutput(); err == nil {
		h.WkhtmlVersion = strings.TrimSpace(string(b))
	}
	return h
}

func cpuModel() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func memInfo() (total, available uint64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			available = kb * 1024
		}
	}
	return total, available
}

func writeReport(path string, rep *report) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func reportFailed(rep *report) bool {
	for _, s := range rep.Cold {
		if s.Error != "" {
			return true
		}
	}
	for _, s := range rep.Warm {
		if s.Error != "" {
			return true
		}
	}
	for _, s := range rep.Throughput {
		if s.Error != "" {
			return true
		}
	}
	for _, p := range rep.Parity {
		if p.Error != "" {
			return true
		}
	}
	return false
}

func printReport(rep *report) {
	writef(os.Stdout, "host: %s | %s | %d cpus | mem %s | %s | %s | go %s | printkit %s\n",
		rep.Host.Uname, rep.Host.CPUModel, rep.Host.CPUs, fmtBytes(rep.Host.MemTotalBytes),
		rep.Host.ChromeVersion, rep.Host.WkhtmlVersion, rep.Host.Go, rep.Host.Printkit)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	writef(tw, "\nCOLD/WARM\tfixture\ttool\tn\twall p50\twall p95\tcpu p50\trss p50\tpss p50\tsize\terr\n")
	for _, s := range rep.Cold {
		writef(tw, "cold\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Fixture, s.Tool, s.N, fmtMS(s.WallP50Ms), fmtMS(s.WallP95Ms), fmtMS(s.CPUP50Ms), fmtBytes(s.RSSP50Bytes), fmtBytes(s.PSSP50Bytes), fmtBytes(uint64(s.SizeP50Bytes)), s.Error)
	}
	for _, s := range rep.Warm {
		writef(tw, "warm\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Fixture, s.Tool, s.N, fmtMS(s.WallP50Ms), fmtMS(s.WallP95Ms), fmtMS(s.CPUP50Ms), fmtBytes(s.RSSP50Bytes), fmtBytes(s.PSSP50Bytes), fmtBytes(uint64(s.SizeP50Bytes)), s.Error)
	}
	writef(tw, "\nTHROUGHPUT\tfixture\ttool\tn\tbatch p50\tdocs/s p50\tdocs/s p95\tcpu/doc p50\trss p50\tpss p50\terr\n")
	for _, s := range rep.Throughput {
		writef(tw, "x%d\t%s\t%s\t%d\t%s\t%.2f\t%.2f\t%s\t%s\t%s\t%s\n",
			s.Parallel, s.Fixture, s.Tool, s.N, fmtMS(s.WallP50Ms), s.DocsPerSecP50, s.DocsPerSecP95, fmtMS(s.CPUP50Ms), fmtBytes(s.RSSP50Bytes), fmtBytes(s.PSSP50Bytes), s.Error)
	}
	_ = tw.Flush()
}

func logSeries(s series) {
	if s.Error != "" {
		logf("  error: %s", s.Error)
		return
	}
	logf("  n=%d wall p50=%s p95=%s cpu p50=%s rss p50=%s pss p50=%s size=%s",
		s.N, fmtMS(s.WallP50Ms), fmtMS(s.WallP95Ms), fmtMS(s.CPUP50Ms), fmtBytes(s.RSSP50Bytes), fmtBytes(s.PSSP50Bytes), fmtBytes(uint64(s.SizeP50Bytes)))
}

func logThroughput(s throughputResult) {
	if s.Error != "" {
		logf("  error: %s", s.Error)
		return
	}
	logf("  batches=%d docs/s p50=%.2f p95=%.2f cpu/doc p50=%s rss p50=%s pss p50=%s",
		s.N, s.DocsPerSecP50, s.DocsPerSecP95, fmtMS(s.CPUP50Ms), fmtBytes(s.RSSP50Bytes), fmtBytes(s.PSSP50Bytes))
}

func fmtMS(ms float64) string {
	if ms <= 0 {
		return "0ms"
	}
	if ms >= 10000 {
		return fmt.Sprintf("%.1fs", ms/1000)
	}
	if ms >= 1000 {
		return fmt.Sprintf("%.2fs", ms/1000)
	}
	return fmt.Sprintf("%.0fms", ms)
}

func fmtBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2fGiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0fMiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fKiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func logf(format string, args ...any) {
	writef(os.Stderr, format+"\n", args...)
}

func fatalf(format string, args ...any) {
	logf(format, args...)
	os.Exit(1)
}
