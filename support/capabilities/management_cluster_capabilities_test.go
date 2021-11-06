package capabilities

import (
	"fmt"
	batchv1 "k8s.io/api/batch/v1"
	"testing"

	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"

	. "github.com/onsi/gomega"
)

func TestIsGroupVersionRegistered(t *testing.T) {

	testCases := []struct {
		name         string
		client       discovery.ServerResourcesInterface
		groupVersion schema.GroupVersion
		resultErr    error
		isRegistered bool
		shouldError  bool
	}{
		{
			name:         "should return false if routes are not registered",
			client:       newFailableFakeDiscoveryClient(nil, hyperv1.GroupVersion, imagev1.GroupVersion),
			groupVersion: routev1.GroupVersion,
			resultErr:    nil,
			isRegistered: false,
			shouldError:  false,
		},
		{
			name:         "should return true if are not registered",
			client:       newFailableFakeDiscoveryClient(nil, hyperv1.GroupVersion, routev1.GroupVersion),
			groupVersion: routev1.GroupVersion,
			resultErr:    nil,
			isRegistered: true,
			shouldError:  false,
		},
		{
			name: "should return true if the requested group causes an error",
			client: newFailableFakeDiscoveryClient(
				&discovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{routev1.GroupVersion: nil}},
			),
			groupVersion: routev1.GroupVersion,
			resultErr:    nil,
			isRegistered: true,
			shouldError:  false,
		},
		{
			name: "should return false if the requested group does not causes an error and does not exist",
			client: newFailableFakeDiscoveryClient(
				&discovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{imagev1.GroupVersion: nil}},
			),
			groupVersion: routev1.GroupVersion,
			resultErr:    nil,
			isRegistered: false,
			shouldError:  false,
		},
		{
			name: "should return true if the requested group does not causes an error but exists in the discovered groups",
			client: newFailableFakeDiscoveryClient(
				&discovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{imagev1.GroupVersion: nil}},
				routev1.GroupVersion,
			),
			groupVersion: routev1.GroupVersion,
			resultErr:    nil,
			isRegistered: true,
			shouldError:  false,
		},
		{
			name: "should fail on arbitrary errors",
			client: newFailableFakeDiscoveryClient(
				fmt.Errorf("ups"),
				routev1.GroupVersion,
			),
			groupVersion: routev1.GroupVersion,
			resultErr:    fmt.Errorf("ups"),
			isRegistered: false,
			shouldError:  true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := isGroupVersionRegistered(tc.client, tc.groupVersion)
			g := NewGomegaWithT(t)
			g.Expect(got).To(Equal(tc.isRegistered))
			if tc.shouldError {
				g.Expect(err).To(Equal(tc.resultErr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestIsObjectKindRegistered(t *testing.T) {

	testCases := []struct {
		name          string
		client        discovery.ServerResourcesInterface
		objectToCheck schema.ObjectKind
		isRegistered  bool
		resultErr     error
		shouldError   bool
	}{
		{
			name: "should find cronjob registered",
			client: addResourcesToFakeDiscoveryClient(fakeFailableDiscoveryClient{
				Resources: []*metav1.APIResourceList{},
			}, &batchv1.CronJob{
				TypeMeta: metav1.TypeMeta{
					Kind:       "CronJob",
					APIVersion: batchv1.SchemeGroupVersion.String(),
				},
			}),
			objectToCheck: &batchv1.CronJob{
				TypeMeta: metav1.TypeMeta{
					Kind:       "CronJob",
					APIVersion: batchv1.SchemeGroupVersion.String(),
				},
			},
			isRegistered: true,
			shouldError:  false,
		},
		{
			name: "should not find cronjob registered",
			client: addResourcesToFakeDiscoveryClient(fakeFailableDiscoveryClient{
				Resources: []*metav1.APIResourceList{},
			}, &batchv1.Job{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Job",
					APIVersion: batchv1.SchemeGroupVersion.String(),
				},
			}),
			objectToCheck: &batchv1.CronJob{
				TypeMeta: metav1.TypeMeta{
					Kind:       "CronJob",
					APIVersion: batchv1.SchemeGroupVersion.String(),
				},
			},
			isRegistered: false,
			shouldError:  false,
		},
		{
			name: "should throw error",
			client: newFailableFakeDiscoveryClient(
				fmt.Errorf("ups"),
				batchv1.SchemeGroupVersion,
			),
			objectToCheck: &batchv1.CronJob{
				TypeMeta: metav1.TypeMeta{
					Kind:       "CronJob",
					APIVersion: batchv1.SchemeGroupVersion.String(),
				},
			},
			isRegistered: false,
			shouldError:  true,
			resultErr:    fmt.Errorf("ups"),
		},
		{
			name: "should not error since group was properly discovered",
			client: newFailableFakeDiscoveryClient(
				&discovery.ErrGroupDiscoveryFailed{
					Groups: map[schema.GroupVersion]error{
						routev1.GroupVersion: fmt.Errorf("blah"),
					},
				},
				routev1.GroupVersion,
			),
			objectToCheck: &batchv1.CronJob{
				TypeMeta: metav1.TypeMeta{
					Kind:       "CronJob",
					APIVersion: batchv1.SchemeGroupVersion.String(),
				},
			},
			isRegistered: false,
			shouldError:  false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := isObjectKindRegistered(tc.client, tc.objectToCheck)
			g := NewGomegaWithT(t)
			g.Expect(got).To(Equal(tc.isRegistered))
			if tc.shouldError {
				g.Expect(err).To(Equal(tc.resultErr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestDetectManagementCapabilities(t *testing.T) {

	testCases := []struct {
		name         string
		client       discovery.ServerResourcesInterface
		groupVersion schema.GroupVersion
		resultErr    error
		isRegistered bool
		shouldError  bool
	}{
		{
			name:         "should return false if routes are not registered",
			client:       newFailableFakeDiscoveryClient(nil, hyperv1.GroupVersion, imagev1.GroupVersion),
			groupVersion: routev1.GroupVersion,
			resultErr:    nil,
			isRegistered: false,
			shouldError:  false,
		},
		{
			name:         "should return true if are not registered",
			client:       newFailableFakeDiscoveryClient(nil, hyperv1.GroupVersion, routev1.GroupVersion),
			groupVersion: routev1.GroupVersion,
			resultErr:    nil,
			isRegistered: true,
			shouldError:  false,
		},
		{
			name: "should fail on arbitrary errors",
			client: newFailableFakeDiscoveryClient(
				fmt.Errorf("ups"),
				routev1.GroupVersion,
			),
			groupVersion: routev1.GroupVersion,
			resultErr:    fmt.Errorf("ups"),
			isRegistered: false,
			shouldError:  true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectManagementClusterCapabilities(tc.client)
			g := NewGomegaWithT(t)
			if tc.shouldError {
				g.Expect(err).To(Equal(tc.resultErr))
			} else {
				g.Expect(got.Has(CapabilityRoute)).To(Equal(tc.isRegistered))
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func newFailableFakeDiscoveryClient(err error, discovered ...schema.GroupVersion) fakeFailableDiscoveryClient {
	discoveryClient := fakeFailableDiscoveryClient{
		Resources: []*metav1.APIResourceList{},
	}
	for _, groupVersion := range discovered {
		discoveryClient.Resources = append(
			discoveryClient.Resources,
			&metav1.APIResourceList{GroupVersion: groupVersion.String()},
		)
	}
	discoveryClient.err = err
	return discoveryClient
}

func addResourcesToFakeDiscoveryClient(discoveryClient fakeFailableDiscoveryClient, objectsToAdd ...schema.ObjectKind) fakeFailableDiscoveryClient {
	for _, objectToAdd := range objectsToAdd {
		resourceListIndex := -1
		found := false
		for apiResourceListIndex, apiResourceList := range discoveryClient.Resources {
			if apiResourceList.GroupVersion == objectToAdd.GroupVersionKind().GroupVersion().String() {
				resourceListIndex = apiResourceListIndex
				for _, apiResource := range apiResourceList.APIResources {
					if apiResource.Kind == objectToAdd.GroupVersionKind().Kind {
						found = true
						break
					}
				}
				break
			}
		}
		if !found {
			if resourceListIndex < 0 {
				discoveryClient.Resources = append(discoveryClient.Resources, &metav1.APIResourceList{
					GroupVersion: objectToAdd.GroupVersionKind().GroupVersion().String(),
					APIResources: []metav1.APIResource{},
				})
				resourceListIndex = len(discoveryClient.Resources) - 1
			}
			discoveryClient.Resources[resourceListIndex].APIResources = append(discoveryClient.Resources[resourceListIndex].APIResources, metav1.APIResource{
				Group:   objectToAdd.GroupVersionKind().Group,
				Kind:    objectToAdd.GroupVersionKind().Kind,
				Version: objectToAdd.GroupVersionKind().Version,
			})
		}
	}
	return discoveryClient
}

// fakeFailableDiscoveryClient is a custom implementation of discovery.ServerResourcesInterface.
// Existing fake clients are not flexible enough to express all resource and error responses relevant for testing.
type fakeFailableDiscoveryClient struct {
	Resources []*metav1.APIResourceList
	err       error
}

func (f fakeFailableDiscoveryClient) ServerResourcesForGroupVersion(_ string) (*metav1.APIResourceList, error) {
	panic("implement me")
}

func (f fakeFailableDiscoveryClient) ServerResources() ([]*metav1.APIResourceList, error) {
	panic("implement me")
}

func (f fakeFailableDiscoveryClient) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, f.Resources, f.err
}

func (f fakeFailableDiscoveryClient) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	panic("implement me")
}

func (f fakeFailableDiscoveryClient) ServerPreferredNamespacedResources() ([]*metav1.APIResourceList, error) {
	panic("implement me")
}
