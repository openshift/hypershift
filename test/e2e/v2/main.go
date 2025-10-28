/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	cmd := &cobra.Command{
		Use:              "test-e2e-v2",
		SilenceUsage:     true,
		TraverseChildren: true,
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
	}

	cmd.AddCommand(newCreateCommand())
	cmd.AddCommand(newTestCommand())

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "create",
		Short:        "Create command",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(0)
		},
	}
}

func newTestCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "test",
		Short:        "Test command",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			os.Exit(0)
		},
	}
}
