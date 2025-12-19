package formatter

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"golang.org/x/tools/imports"
)

const (
	defaultFormatter = "goimports"
	gofmt            = "gofmt"
)

type Executable struct {
	// Some extra params around the base formatter,
	// generated from the BaseFormatterCmd argument in the config.
	cmd  string
	args []string

	skip bool
}

func NewExecutable(rawCmd string) *Executable {
	switch rawCmd {
	// defaults to goimports (if found), otherwise skip it.
	case defaultFormatter, "":
		_, err := exec.LookPath(defaultFormatter)
		if err != nil {
			// It will use gofmt, the default internal formatter.
			return &Executable{skip: true}
		}

		return &Executable{cmd: defaultFormatter}

	// gofmt is the default internal formatter.
	case gofmt:
		return &Executable{skip: true}
	}

	parts := strings.Fields(rawCmd)

	e := &Executable{cmd: parts[0]}

	if len(parts) > 1 {
		e.args = parts[1:]
	}

	return e
}

func (e *Executable) Format(ctx context.Context, src []byte) ([]byte, error) {
	if e.skip {
		return src, nil
	}

	// This is a special case:
	// Instead of running the `goimports` as a separate process,
	// call the `imports` package directly.
	//
	// Note(ldez): the `exec.LookPath` is kept inside the NewExecutable constructor,
	// for compatibility with the existing behavior,
	// but this can be changed in the future.
	if e.cmd == defaultFormatter && len(e.args) == 0 {
		return imports.Process("", src, nil)
	}

	return e.exec(ctx, src)
}

// IsGofmtCompliant returns true if the formatter is compliant with gofmt.
//
// The goal is to avoid formatting after the Shortener if it's not necessary:
//   - `gofmt` is run by the Shortener
//   - `goimports` is based on `gofmt` but also changes the order of the imports,
//     but the Shortener doesn't change the order of the imports.
func (e *Executable) IsGofmtCompliant() bool {
	return e.cmd == defaultFormatter || e.cmd == gofmt
}

func (e *Executable) exec(ctx context.Context, src []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.cmd, e.args...)

	slog.Debug("Running", slog.String("cmd", cmd.String()))

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	outBuffer := &bytes.Buffer{}
	cmd.Stdout = outBuffer

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cmd.String(), err)
	}

	_, err = stdinPipe.Write(src)
	if err != nil {
		return nil, fmt.Errorf("write to stdin %s: %w", cmd.String(), err)
	}

	_ = stdinPipe.Close()

	err = cmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("wait %s: %w", cmd.String(), err)
	}

	return outBuffer.Bytes(), nil
}
