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

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"

	"github.com/openshift/hypershift/cmd/consolelogs"
	createcmd "github.com/openshift/hypershift/cmd/create"
	destroycmd "github.com/openshift/hypershift/cmd/destroy"
	dumpcmd "github.com/openshift/hypershift/cmd/dump"
	installcmd "github.com/openshift/hypershift/cmd/install"
	cliversion "github.com/openshift/hypershift/cmd/version"
	"github.com/openshift/hypershift/pkg/version"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))

	cmd := &cobra.Command{
		Use:              "hypershift",
		SilenceUsage:     true,
		TraverseChildren: true,

		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
	}

	cmd.Version = version.String()

	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	cmd.AddCommand(installcmd.NewCommand())
	cmd.AddCommand(createcmd.NewCommand())
	cmd.AddCommand(destroycmd.NewCommand())
	cmd.AddCommand(dumpcmd.NewCommand())
	cmd.AddCommand(consolelogs.NewCommand())
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
