package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tushar1210/printkit"
)

func envOn(key string) bool {
	v := strings.TrimSpace(os.Getenv(key))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func defaultSocket() string {
	if v := strings.TrimSpace(os.Getenv("PRINTKIT_SOCKET")); v != "" {
		return v
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("printkit-%d.sock", os.Getuid()))
}

func stripFlag(args []string, name string) ([]string, bool) {
	var out []string
	found := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			out = append(out, args[i:]...)
			break
		}
		if args[i] == name {
			found = true
			continue
		}
		out = append(out, args[i])
	}
	return out, found
}

func serve(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signalContext()
	defer stop()
	return serveContext(ctx, args, stdout, stderr)
}

func serveContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("htmlprint serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", defaultSocket(), "unix socket path")
	listen := fs.String("listen", "", "loopback TCP address, for example 127.0.0.1:9291")
	chrome := fs.String("chrome-path", "", "Chromium binary")
	noSandbox := fs.Bool("no-sandbox", false, "pass --no-sandbox to Chromium")
	pool := fs.Int("pool", 4, "tab pool size")
	fs.Usage = func() {
		writef(stderr, "htmlprint serve [--socket path] [--listen 127.0.0.1:port] [--pool n] [--chrome-path path] [--no-sandbox]\n")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var opts []printkit.Option
	opts = append(opts, printkit.WithPoolSize(*pool), printkit.WithDefaultTimeout(5*time.Minute))
	if *chrome != "" {
		opts = append(opts, printkit.WithChromePath(*chrome))
	}
	if *noSandbox {
		opts = append(opts, printkit.WithNoSandbox(true))
	}
	conv, err := printkit.New(opts...)
	if err != nil {
		writef(stderr, "htmlprint: %s\n", err)
		return 1
	}
	defer func() { _ = conv.Close() }()

	ln, addr, err := listenDaemon(*listen, *socket)
	if err != nil {
		writef(stderr, "htmlprint: %s\n", err)
		return 1
	}
	defer func() { _ = ln.Close() }()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/render", func(w http.ResponseWriter, r *http.Request) {
		renderDaemon(conv, w, r)
	})
	srv := &http.Server{Handler: mux}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	writef(stdout, "htmlprint: serving %s\n", addr)
	select {
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		return 0
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			writef(stderr, "htmlprint: %s\n", err)
			return 1
		}
		return 0
	}
}

func listenDaemon(listen, socket string) (net.Listener, string, error) {
	if listen != "" {
		host, _, err := net.SplitHostPort(listen)
		if err != nil || !isLoopback(host) {
			return nil, "", fmt.Errorf("--listen must be a loopback host:port")
		}
		ln, err := net.Listen("tcp", listen)
		if err != nil {
			return nil, "", err
		}
		return ln, ln.Addr().String(), nil
	}
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, "", err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	return ln, socket, nil
}

func isLoopback(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type daemonRequest struct {
	Argv0 string   `json:"argv0"`
	Args  []string `json:"args"`
}

func renderDaemon(conv *printkit.Converter, w http.ResponseWriter, r *http.Request) {
	var body daemonRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cmd, err := Parse(body.Argv0, body.Args)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if cmd.Help || cmd.Version || cmd.DumpTOC {
		http.Error(w, "help and version are local", http.StatusBadRequest)
		return
	}
	var buf bytes.Buffer
	ctx := r.Context()
	cmd.Output = "-"
	switch cmd.Mode {
	case "image":
		err = writeImage(ctx, conv, cmd, &buf)
	default:
		err = writePDF(ctx, conv, cmd, &buf)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Printkit-Daemon", "1")
	_, _ = w.Write(buf.Bytes())
}

// renderViaDaemon sends the invocation to a running daemon.
// handled is false only when the socket cannot be dialed, so the caller can
// fall back to a cold Chromium. A daemon that accepts the connection and then
// fails is handled, and is not rendered a second time.
func renderViaDaemon(argv0 string, args []string, cmd Command, stdout, stderr io.Writer) (int, bool) {
	client, endpoint, ok := dialDaemon()
	if !ok {
		return 0, false
	}
	payload, err := json.Marshal(daemonRequest{Argv0: argv0, Args: args})
	if err != nil {
		writef(stderr, "htmlprint: daemon: %s\n", err)
		return 1, true
	}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(payload))
	if err != nil {
		writef(stderr, "htmlprint: daemon: %s\n", err)
		return 1, true
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		writef(stderr, "htmlprint: daemon: %s\n", err)
		return 1, true
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if msg == "" {
			msg = resp.Status
		}
		writef(stderr, "htmlprint: %s\n", msg)
		if resp.StatusCode == http.StatusBadRequest {
			return 2, true
		}
		return 1, true
	}
	if cmd.Output == "-" || cmd.Output == "" {
		if _, err := stdout.Write(data); err != nil {
			writef(stderr, "htmlprint: %s\n", err)
			return 1, true
		}
		return 0, true
	}
	if err := writeAtomic(cmd.Output, data); err != nil {
		writef(stderr, "htmlprint: %s\n", err)
		return 1, true
	}
	return 0, true
}

// dialDaemon returns a client when a daemon accepts a connection.
// PRINTKIT_LISTEN selects a loopback TCP listener; otherwise the unix socket
// from PRINTKIT_SOCKET or the per-user default is used.
func dialDaemon() (*http.Client, string, bool) {
	if addr := strings.TrimSpace(os.Getenv("PRINTKIT_LISTEN")); addr != "" {
		dialer := net.Dialer{Timeout: 200 * time.Millisecond}
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, "", false
		}
		_ = conn.Close()
		return &http.Client{}, "http://" + addr + "/v1/render", true
	}
	sock := defaultSocket()
	dialer := net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := dialer.Dial("unix", sock)
	if err != nil {
		return nil, "", false
	}
	_ = conn.Close()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}
	return &http.Client{Transport: transport}, "http://printkit/v1/render", true
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".printkit-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
