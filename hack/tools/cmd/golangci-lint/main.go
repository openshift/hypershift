package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	_ "github.com/openshift/hypershift/hack/tools/hypershiftlinter"

	_ "sigs.k8s.io/kube-api-linter"

	"github.com/golangci/golangci-lint/v2/pkg/commands"
)

func main() {
	info := commands.BuildInfo{GoVersion: runtime.Version()}
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range buildInfo.Deps {
			if dependency.Path == "github.com/golangci/golangci-lint/v2" {
				info.Version = strings.TrimPrefix(dependency.Version, "v")
				break
			}
		}
	}
	if err := commands.Execute(info); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
