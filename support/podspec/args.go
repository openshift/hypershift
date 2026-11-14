package podspec

import "strings"

// RemoveArgsWithPrefix returns args with any element that starts with one of the given
// prefixes removed. Used to drop single-element flags like "--config=..." that an older
// operand binary does not understand.
func RemoveArgsWithPrefix(args []string, prefixes ...string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		drop := false
		for _, p := range prefixes {
			if strings.HasPrefix(a, p) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, a)
		}
	}
	return out
}

// RemoveFlagsWithValues returns args with each named flag and the element immediately
// following it removed (the two-element "--flag value" form). Used to drop flags like
// "--haproxy-image", "3.2=..." that an older operand binary does not understand.
func RemoveFlagsWithValues(args []string, flags ...string) []string {
	isFlag := make(map[string]struct{}, len(flags))
	for _, f := range flags {
		isFlag[f] = struct{}{}
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if _, ok := isFlag[args[i]]; ok {
			i++ // also skip the value element
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// RemoveShellScriptArgLines removes, from each arg, any whole line containing one of the given
// substrings. Used for operands whose flags are embedded in a "-c" shell-script arg (flags on
// backslash-continued lines); dropping a continued middle line is safe because the preceding
// line keeps its trailing backslash.
func RemoveShellScriptArgLines(args []string, substrs ...string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if !strings.Contains(a, "\n") {
			out[i] = a
			continue
		}
		lines := strings.Split(a, "\n")
		kept := make([]string, 0, len(lines))
		for _, line := range lines {
			drop := false
			for _, s := range substrs {
				if strings.Contains(line, s) {
					drop = true
					break
				}
			}
			if !drop {
				kept = append(kept, line)
			}
		}
		out[i] = strings.Join(kept, "\n")
	}
	return out
}
