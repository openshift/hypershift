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

	createcmd "github.com/openshift/hypershift/cmd/create"
	destroycmd "github.com/openshift/hypershift/cmd/destroy"
	installcmd "github.com/openshift/hypershift/cmd/install"
)

func main() {
	cmd := &cobra.Command{
		Use: "hypershift",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}
	cmd.AddCommand(installcmd.NewCommand())
	cmd.AddCommand(createcmd.NewCommand())
	cmd.AddCommand(destroycmd.NewCommand())

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
