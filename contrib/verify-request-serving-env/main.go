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

	reqserving "github.com/openshift/hypershift/test/e2e/util/reqserving"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	// Initialize a dev-mode logger so VerifyRequestServingEnvironment emits progress logs
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("verify-request-serving-env"))

	fmt.Println("Running VerifyRequestServingEnvironment against the current Kubernetes context...")
	if err := reqserving.VerifyRequestServingEnvironment(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Verification FAILED: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Verification PASSED")
}
