package printkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// FindChrome returns the Chromium or Chrome binary to launch.
//
// The search order is CHROME_PATH, CHROMIUM_PATH, CHROME_BIN, then the usual
// install locations for the current operating system. WithChromePath skips
// this search.
func FindChrome() (string, error) {
	for _, env := range []string{"CHROME_PATH", "CHROMIUM_PATH", "CHROME_BIN"} {
		v := os.Getenv(env)
		if v == "" {
			continue
		}
		if path, ok := existingBinary(v); ok {
			return path, nil
		}
		return "", optionf("ChromePath", "%s=%s does not point at a binary", env, v)
	}
	// chrome-headless-shell is the smaller headless binary shipped with Chrome.
	// WithChromePath still wins, because New does not call FindChrome when it
	// is set. A full Chrome is the fallback when the shell is not installed.
	for _, candidate := range append(headlessShellCandidates(), chromeCandidates()...) {
		if path, ok := existingBinary(candidate); ok {
			return path, nil
		}
	}
	return "", ErrChromeNotFound
}

func existingBinary(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if filepath.IsAbs(path) {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return "", false
		}
		return path, true
	}
	found, err := exec.LookPath(path)
	if err != nil {
		return "", false
	}
	return found, true
}

func headlessShellCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/chrome-headless-shell",
			"/Applications/Chromium.app/Contents/MacOS/chrome-headless-shell",
			"chrome-headless-shell",
		}
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		prog := os.Getenv("PROGRAMFILES")
		prog86 := os.Getenv("PROGRAMFILES(X86)")
		return []string{
			filepath.Join(prog, `Google\Chrome\Application\chrome-headless-shell.exe`),
			filepath.Join(prog86, `Google\Chrome\Application\chrome-headless-shell.exe`),
			filepath.Join(local, `Google\Chrome\Application\chrome-headless-shell.exe`),
			"chrome-headless-shell",
			"chrome-headless-shell.exe",
		}
	default:
		return []string{
			"chrome-headless-shell",
			"/opt/google/chrome/chrome-headless-shell",
			"/opt/google/chrome-unstable/chrome-headless-shell",
			"/usr/lib/chromium/chrome-headless-shell",
			"/usr/lib/chromium-browser/chrome-headless-shell",
			"/usr/local/bin/chrome-headless-shell",
		}
	}
}

func chromeCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"google-chrome",
			"chromium",
		}
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		prog := os.Getenv("PROGRAMFILES")
		prog86 := os.Getenv("PROGRAMFILES(X86)")
		return []string{
			filepath.Join(prog, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(prog86, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(local, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(prog, `Microsoft\Edge\Application\msedge.exe`),
			"chrome",
			"chrome.exe",
			"msedge",
			"msedge.exe",
		}
	default:
		return []string{
			"google-chrome-stable",
			"google-chrome",
			"chromium",
			"chromium-browser",
			"google-chrome-beta",
			"google-chrome-unstable",
			"microsoft-edge",
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/local/bin/google-chrome",
			"/usr/local/bin/chromium",
			"/snap/bin/chromium",
		}
	}
}
