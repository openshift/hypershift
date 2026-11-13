package reporter

import (
	"io"

	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/scanner"
)

// Reporter formats and writes an Inventory to the given writer.
type Reporter interface {
	Report(w io.Writer, inv *scanner.Inventory) error
}

// New returns a Reporter for the given format string.
// Supported formats: "text" (default), "json", "markdown".
func New(format string) Reporter {
	switch format {
	case "json":
		return &JSONReporter{}
	case "markdown":
		return &MarkdownReporter{}
	default:
		return &TextReporter{}
	}
}
