package printkit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindChromeOverride(t *testing.T) {
	t.Setenv("CHROME_PATH", filepath.Join(t.TempDir(), "missing-chrome"))
	t.Setenv("CHROMIUM_PATH", "")
	t.Setenv("CHROME_BIN", "")
	_, err := FindChrome()
	if !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("missing override: %v", err)
	}
}

func TestFindChromeDiscovers(t *testing.T) {
	t.Setenv("CHROME_PATH", "")
	t.Setenv("CHROMIUM_PATH", "")
	t.Setenv("CHROME_BIN", "")
	path, err := FindChrome()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		t.Fatalf("discovered %s: %v", path, err)
	}
}
