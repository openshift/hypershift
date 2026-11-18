package etcdsanfix

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// step is one SRE-gated step rendered in the six-beat form:
// observe (observed) -> show (read) -> verify-yourself (verify) -> propose (action,
// consequence) -> gate (token or y/N). The caller does the act + re-observe.
type step struct {
	title       string
	observed    []string
	read        string
	verify      []string
	action      string
	consequence string
	// token, when non-empty, requires the SRE to type it exactly to proceed
	// (used for destructive/outward-facing steps). Empty means a y/N gate.
	token string
}

// confirm renders s to out and reads the decision from in. It returns true only on
// explicit approval: "y" for a y/N gate, or the exact token for a token-gated step.
// Anything else, including EOF, is a firm no.
func confirm(in io.Reader, out io.Writer, s step) (bool, error) {
	renderStep(out, s)
	want, prompt := "y", "Proceed? [y/N]: "
	if s.token != "" {
		want = s.token
		prompt = fmt.Sprintf("Type %q to proceed, or anything else to skip: ", s.token)
	}
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		if err == io.EOF {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(line) == want, nil
}

func renderStep(out io.Writer, s step) {
	fmt.Fprintf(out, "\n=== %s ===\n", s.title)
	if len(s.observed) > 0 {
		fmt.Fprintln(out, "OBSERVED:")
		for _, o := range s.observed {
			fmt.Fprintf(out, "  %s\n", o)
		}
	}
	if s.read != "" {
		fmt.Fprintf(out, "MY READ: %s\n", s.read)
	}
	if len(s.verify) > 0 {
		fmt.Fprintln(out, "VERIFY YOURSELF:")
		for _, v := range s.verify {
			fmt.Fprintf(out, "  $ %s\n", v)
		}
	}
	if s.action != "" {
		fmt.Fprintf(out, "NEXT: %s\n", s.action)
	}
	if s.consequence != "" {
		fmt.Fprintf(out, "CONSEQUENCE: %s\n", s.consequence)
	}
}
