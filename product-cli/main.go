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
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	cliversion "github.com/openshift/hypershift/cmd/version"
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/product-cli/cmd/create"
	"github.com/openshift/hypershift/product-cli/cmd/destroy"
)

func main() {
	cmd := &cobra.Command{
		Use:              "hcp",
		SilenceUsage:     true,
		TraverseChildren: true,

		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	cmd.AddCommand(create.NewCommand())
	cmd.AddCommand(destroy.NewCommand())
	cmd.AddCommand(cliversion.NewVersionCommand())

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	go func() {
		<-sigs
		_, _ = fmt.Fprintln(os.Stderr, "\nAborted...")
		cancel()
	}()

	if err := cmd.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
