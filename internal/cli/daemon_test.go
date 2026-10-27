package cli

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUseDaemonHelpSkipsChrome(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := Run("htmlprint", []string{"--use-daemon", "--help"}, &out, &errBuf)
	if code != 0 || !strings.Contains(out.String(), "htmlprint serve") {
		t.Fatalf("code %d\n%s\n%s", code, out.String(), errBuf.String())
	}
}

func TestParseBlockRemote(t *testing.T) {
	cmd, err := Parse("htmlprint", []string{"--block-remote", "--no-outline", "a.html", "out.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.PDF.Network.BlockRemote == nil || !*cmd.PDF.Network.BlockRemote {
		t.Fatalf("network %#v", cmd.PDF.Network)
	}
	if cmd.PDF.Outline.Enabled == nil || *cmd.PDF.Outline.Enabled {
		t.Fatalf("outline %#v", cmd.PDF.Outline)
	}
}

func TestDaemonHangupDoesNotFallBack(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "printkit.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	t.Setenv("PRINTKIT_SOCKET", sock)
	t.Setenv("PRINTKIT_DAEMON", "1")
	in := filepath.Join(dir, "in.html")
	if err := os.WriteFile(in, []byte("<p>fallback</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pdf")
	var stdout, stderr bytes.Buffer
	code := Run("htmlprint", []string{in, out}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("daemon hangup fell back to a successful cold render")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("cold fallback wrote the output")
	}
	if !strings.Contains(stderr.String(), "daemon") {
		t.Fatalf("stderr %q", stderr.String())
	}
}

func TestDaemonRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "printkit.sock")
	t.Setenv("PRINTKIT_SOCKET", sock)
	if os.Geteuid() != 0 {
		t.Setenv("PRINTKIT_NO_SANDBOX", "1")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan int, 1)
	go func() {
		args := []string{"--socket", sock, "--pool", "1"}
		if os.Getenv("PRINTKIT_NO_SANDBOX") == "1" || os.Geteuid() == 0 {
			args = append(args, "--no-sandbox")
		}
		var stdout, stderr bytes.Buffer
		serveDone <- serveContext(ctx, args, &stdout, &stderr)
	}()
	deadline := time.Now().Add(40 * time.Second)
	up := false
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			_ = conn.Close()
			up = true
			break
		}
		select {
		case code := <-serveDone:
			t.Fatalf("serve exited %d before listening", code)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !up {
		t.Fatal("daemon socket did not come up")
	}
	in := filepath.Join(dir, "in.html")
	if err := os.WriteFile(in, []byte("<h1>Daemon Chapter</h1><p>DAEMONMARK</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pdf")
	var stdout, stderr bytes.Buffer
	code := Run("htmlprint", []string{"--use-daemon", in, out}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("client %d: %s", code, stderr.String())
	}
	pdf, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("prefix %q", pdf[:min(8, len(pdf))])
	}
	text, err := exec.Command("pdftotext", out, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(text, []byte("DAEMONMARK")) {
		t.Fatalf("text %s", text)
	}
	cancel()
	select {
	case <-serveDone:
	case <-time.After(8 * time.Second):
		t.Fatal("serve did not stop")
	}
}

func TestListenRejectsPublicHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := serveContext(ctx, []string{"--listen", "8.8.8.8:9"}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "loopback") {
		t.Fatalf("code %d stderr %q", code, stderr.String())
	}
}
