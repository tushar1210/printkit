package printkit

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// assetServer serves local HTML and the directories next to it on 127.0.0.1
// so Chromium never has to open file:// URLs.
type assetServer struct {
	srv   *http.Server
	addr  string
	mu    sync.Mutex
	dirs  map[string]string
	blobs map[string][]byte
}

func newAssetServer() (*assetServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("printkit: listen: %w", err)
	}
	a := &assetServer{
		addr:  ln.Addr().String(),
		dirs:  map[string]string{},
		blobs: map[string][]byte{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.route)
	a.srv = &http.Server{Handler: mux}
	go func() { _ = a.srv.Serve(ln) }()
	return a, nil
}

func (a *assetServer) close() error {
	if a == nil || a.srv == nil {
		return nil
	}
	return a.srv.Close()
}

func (a *assetServer) route(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	kind, id := parts[0], parts[1]
	switch kind {
	case "b":
		a.mu.Lock()
		body, ok := a.blobs[id]
		a.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	case "d":
		a.mu.Lock()
		root, ok := a.dirs[id]
		a.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		rel := strings.Join(parts[2:], "/")
		full, err := safeJoin(root, rel)
		if err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, full)
	default:
		http.NotFound(w, r)
	}
}

func safeJoin(root, rel string) (string, error) {
	root = filepath.Clean(root)
	if rel == "" || rel == "." {
		return root, nil
	}
	full := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", ErrFileAccessDenied
	}
	return full, nil
}

type mounted struct {
	url     string
	cleanup func()
}

func (a *assetServer) mountHTML(html string) mounted {
	id := newID()
	a.mu.Lock()
	a.blobs[id] = []byte(html)
	a.mu.Unlock()
	return mounted{
		url: "http://" + a.addr + "/b/" + id + "/",
		cleanup: func() {
			a.mu.Lock()
			delete(a.blobs, id)
			a.mu.Unlock()
		},
	}
}

func (a *assetServer) mountDir(dir, name string) (mounted, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return mounted{}, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return mounted{}, fmt.Errorf("printkit: base directory %s: %w", dir, err)
	}
	id := newID()
	a.mu.Lock()
	a.dirs[id] = abs
	a.mu.Unlock()
	u := "http://" + a.addr + "/d/" + id + "/"
	if name != "" {
		u += url.PathEscape(name)
	}
	return mounted{
		url: u,
		cleanup: func() {
			a.mu.Lock()
			delete(a.dirs, id)
			a.mu.Unlock()
		},
	}, nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func readAllLimited(r io.Reader) ([]byte, error) {
	const max = 64 << 20
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if len(b) > max {
		return nil, optionf("Reader", "document exceeds %d bytes", max)
	}
	return b, nil
}
