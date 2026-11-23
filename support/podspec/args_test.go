package podspec

import (
	"reflect"
	"testing"
)

func TestRemoveArgsWithPrefix(t *testing.T) {
	args := []string{"--foo=1", "--config=/path/config.yaml", "--bar"}
	got := RemoveArgsWithPrefix(args, "--config=")
	want := []string{"--foo=1", "--bar"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRemoveFlagsWithValues(t *testing.T) {
	args := []string{
		"start",
		"--haproxy-image", "3.2=img",
		"--keep", "val",
		"--default-haproxy-version", "3.2",
		"--deprecated-haproxy-version", "",
	}
	got := RemoveFlagsWithValues(args, "--haproxy-image", "--default-haproxy-version", "--deprecated-haproxy-version")
	want := []string{"start", "--keep", "val"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRemoveShellScriptArgLines(t *testing.T) {
	script := "exec /usr/bin/op \\\n  --config=/path/config.yaml \\\n  --files=/path/config.yaml \\\n  --other"
	got := RemoveShellScriptArgLines([]string{"-c", script}, "--config=")
	// The --config line is dropped; --files (which also references config.yaml) is kept,
	// and the non-script "-c" arg is untouched.
	wantScript := "exec /usr/bin/op \\\n  --files=/path/config.yaml \\\n  --other"
	want := []string{"-c", wantScript}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}
