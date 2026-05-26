package k8sutil

import (
	"testing"

	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestGetHostedClusterManagedResources(t *testing.T) {
	tests := []struct {
		name               string
		platformsInstalled string
		expectedObjectsSet sets.Set[client.Object]
	}{
		{
			name:               "When no platforms are installed it should return no resources",
			platformsInstalled: "",
			expectedObjectsSet: sets.Set[client.Object]{},
		},
		{
			name:               "When only the None platform is installed it should return no resources",
			platformsInstalled: "None",
			expectedObjectsSet: sets.Set[client.Object]{},
		},
		{
			name:               "When only the AWS platform is installed it should return only AWS resources",
			platformsInstalled: "AWS",
			expectedObjectsSet: sets.New[client.Object](append(BaseResources, AWSResources...)...),
		},
		{
			name:               "When the Azure and AWS platforms are installed it should return both resource sets",
			platformsInstalled: "azure,AWS",
			expectedObjectsSet: sets.New[client.Object](
				append(append(BaseResources, AWSResources...), AzureResources...)...,
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			actualObjects := GetHostedClusterManagedResources(tc.platformsInstalled)
			actualObjectSet := sets.New[client.Object](actualObjects...)
			g.Expect(actualObjectSet).To(Equal(tc.expectedObjectsSet))
		})
	}
}

func TestGetNodePoolManagedResources(t *testing.T) {
	tests := []struct {
		name               string
		platformsInstalled string
		expectedObjectsSet sets.Set[client.Object]
	}{
		{
			name:               "When no platforms are installed it should return no resources",
			platformsInstalled: "",
			expectedObjectsSet: sets.Set[client.Object]{},
		},
		{
			name:               "When only the AWS platform is installed it should return only AWS NodePool resources",
			platformsInstalled: "AWS",
			expectedObjectsSet: sets.New[client.Object](AWSNodePoolResources...),
		},
		{
			name:               "When the Azure platform is installed it should return only Azure NodePool resources",
			platformsInstalled: "azure",
			expectedObjectsSet: sets.New[client.Object](AzureNodePoolResources...),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			actualObjects := GetNodePoolManagedResources(tc.platformsInstalled)
			actualObjectSet := sets.New[client.Object](actualObjects...)
			g.Expect(actualObjectSet).To(Equal(tc.expectedObjectsSet))
		})
	}
}
