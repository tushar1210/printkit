// Command htmlprint renders HTML to PDF or an image with headless Chromium.
//
// It is a thin wrapper around the printkit module and accepts the wkhtmltopdf
// and wkhtmltoimage flags that still matter for scripts. Run htmlprint --help
// for the list. Flags that wkhtmltopdf accepted but that this tool does not
// implement are rejected with a reason, rather than ignored.
package main

import (
	"os"

	"github.com/tushar1210/printkit/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[0], os.Args[1:], os.Stdout, os.Stderr))
}
