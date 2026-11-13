package main

import (
	"fmt"
	"os"

	"github.com/openshift/hypershift/contrib/hcp-infra/cmd"
)

var version = "dev"

func main() {
	rootCmd := cmd.NewRootCommand(version)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}
}
