package diff

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	rpdiff "github.com/rogpeppe/go-internal/diff"
	"golang.org/x/term"
)

const (
	ansiGreen = "\033[92m"
	ansiRed   = "\033[91m"
	ansiBlue  = "\033[94m"
	ansiEnd   = "\033[0m"
)

// Pretty prints colored, git-style diffs to the console.
func Pretty(path string, content, result []byte) []byte {
	if bytes.Equal(content, result) {
		return nil
	}

	patch := rpdiff.Diff(path, content, path+".shortened", result)

	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return patch
	}

	var builder bytes.Buffer

	for line := range strings.Lines(string(patch)) {
		switch {
		case strings.HasPrefix(line, "+"):
			_, _ = fmt.Fprint(&builder, ansiGreen, line, ansiEnd)

		case strings.HasPrefix(line, "-"):
			_, _ = fmt.Fprint(&builder, ansiRed, line, ansiEnd)

		case strings.HasPrefix(line, "^"):
			_, _ = fmt.Fprint(&builder, ansiBlue, line, ansiEnd)

		default:
			_, _ = fmt.Fprintf(&builder, "%s", line)
		}
	}

	_, _ = fmt.Fprintln(&builder)

	return builder.Bytes()
}
