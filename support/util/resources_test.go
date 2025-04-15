package util

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/google/go-cmp/cmp"
)

func TestGetHostedClusterManagedResources(t *testing.T) {
	testCases := []struct {
		name               string
		platformsInstalled string
		expectedObjectsSet sets.Set[client.Object]
	}{
		{
			name:               "when no platforms are installed, expect no resources",
			platformsInstalled: "",
			expectedObjectsSet: sets.Set[client.Object]{},
		},
		{
			name:               "when only the None platform is installed, expect no resources",
			platformsInstalled: "None",
			expectedObjectsSet: sets.Set[client.Object]{},
		},
		{
			name:               "when only the AWS platform is installed, expect only AWS resources",
			platformsInstalled: "AWS",
			expectedObjectsSet: sets.New[client.Object](append(BaseResources, AWSResources...)...),
		},
		{
			name:               "when the Azure and AWS platforms are installed, expect only Azure and AWS resources",
			platformsInstalled: "azure,AWS", // testing case insensitivity here
			expectedObjectsSet: sets.New[client.Object](
				append(append(BaseResources, AWSResources...), AzureResources...)...,
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actualObjects := GetHostedClusterManagedResources(tc.platformsInstalled)
			actualObjectSet := sets.New[client.Object](actualObjects...)

			if diff := cmp.Diff(actualObjectSet, tc.expectedObjectsSet); diff != "" {
				t.Errorf("the set of managed resources did not match expected set of managed resources: %s", diff)
			}
		})
	}
}
