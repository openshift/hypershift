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
	"flag"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/util/reqserving"

	"k8s.io/apimachinery/pkg/types"
)

func main() {
	var (
		clusterName      = flag.String("cluster-name", "", "Name of the HostedCluster to verify")
		clusterNamespace = flag.String("cluster-namespace", "", "Namespace of the HostedCluster to verify")
	)
	flag.Parse()

	if *clusterName == "" || *clusterNamespace == "" {
		fmt.Fprintf(os.Stderr, "Error: Both --cluster-name and --cluster-namespace are required\n")
		flag.Usage()
		os.Exit(1)
	}

	ctx := context.Background()

	// Get cluster client
	client, err := e2eutil.GetClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get cluster client: %v\n", err)
		os.Exit(1)
	}

	// Get the HostedCluster
	hostedCluster := &hyperv1.HostedCluster{}
	key := types.NamespacedName{
		Name:      *clusterName,
		Namespace: *clusterNamespace,
	}
	if err := client.Get(ctx, key, hostedCluster); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get HostedCluster %s/%s: %v\n", *clusterNamespace, *clusterName, err)
		os.Exit(1)
	}

	fmt.Printf("Verifying request serving configuration for HostedCluster %s/%s\n\n", *clusterNamespace, *clusterName)

	// Define verification functions to run
	verifications := []struct {
		name string
		fn   func(context.Context, *hyperv1.HostedCluster) error
	}{
		{"Control Plane Effects", reqserving.VerifyRequestServingCPEffects},
		{"Node Allocation", reqserving.VerifyRequestServingNodeAllocation},
		{"Pod Distribution", reqserving.VerifyRequestServingPodDistribution},
		{"Placeholder ConfigMaps", reqserving.VerifyRequestServingPlaceholderConfigMaps},
		{"Pod Labels", reqserving.VerifyRequestServingPodLabels},
	}

	var failed bool
	// Run each verification
	for _, verification := range verifications {
		fmt.Printf("Running verification: %s... ", verification.name)
		if err := verification.fn(ctx, hostedCluster); err != nil {
			fmt.Printf("FAILED\n")
			fmt.Printf("  Error: %v\n\n", err)
			failed = true
		} else {
			fmt.Printf("PASSED\n")
		}
	}

	fmt.Printf("\nVerification Summary:\n")
	if failed {
		fmt.Printf("❌ Some verifications failed. See details above.\n")
		os.Exit(1)
	} else {
		fmt.Printf("✅ All verifications passed successfully!\n")
	}
}
