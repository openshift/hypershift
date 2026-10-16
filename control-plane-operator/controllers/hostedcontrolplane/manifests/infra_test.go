package manifests

import (
	"testing"

	. "github.com/onsi/gomega"

	cpomanifests "github.com/openshift/hypershift/pkg/manifests/cpo"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
)

func TestServiceFunctions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		buildFunc    func(string) *corev1.Service
		expectedName string
		expectedApp  string
	}{
		{
			name:         "When KubeAPIServerServiceAzureLB is called, it should set namespace correctly",
			buildFunc:    cpomanifests.KubeAPIServerServiceAzureLB,
			expectedName: "kube-apiserverlb",
		},
		{
			name:         "When KubeAPIServerService is called, it should set namespace correctly",
			buildFunc:    cpomanifests.KubeAPIServerService,
			expectedName: "kube-apiserver",
		},
		{
			name:         "When KubeAPIServerPrivateService is called, it should set namespace correctly",
			buildFunc:    KubeAPIServerPrivateService,
			expectedName: "kube-apiserver-private",
		},
		{
			name:         "When KubeAPIServerExternalPrivateService is called, it should set namespace correctly",
			buildFunc:    cpomanifests.KubeAPIServerExternalPrivateService,
			expectedName: "kube-apiserver-private-external",
		},
		{
			name:         "When OauthServerService is called, it should set namespace correctly",
			buildFunc:    cpomanifests.OauthServerService,
			expectedName: "oauth-openshift",
			expectedApp:  "oauth-openshift",
		},
		{
			name:         "When OauthServerExternalPrivateService is called, it should set namespace correctly",
			buildFunc:    cpomanifests.OauthServerExternalPrivateService,
			expectedName: "oauth-private-external",
		},
		{
			name:         "When KonnectivityServerService is called, it should set namespace correctly",
			buildFunc:    KonnectivityServerService,
			expectedName: "konnectivity-server",
			expectedApp:  "konnectivity-server",
		},
		{
			name:         "When OpenshiftAPIServerService is called, it should set namespace correctly",
			buildFunc:    OpenshiftAPIServerService,
			expectedName: "openshift-apiserver",
		},
		{
			name:         "When OauthAPIServerService is called, it should set namespace correctly",
			buildFunc:    OauthAPIServerService,
			expectedName: "openshift-oauth-apiserver",
		},
		{
			name:         "When OLMPackageServerService is called, it should set namespace correctly",
			buildFunc:    OLMPackageServerService,
			expectedName: "packageserver",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			namespace := "test-namespace"
			service := tc.buildFunc(namespace)

			g.Expect(service.Namespace).To(Equal(namespace))
			g.Expect(service.Name).To(Equal(tc.expectedName))
			if tc.expectedApp != "" {
				g.Expect(service.Labels).To(HaveKeyWithValue("app", tc.expectedApp))
			}
		})
	}
}

func TestRouteFunctions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		buildFunc    func(string) *routev1.Route
		expectedName string
	}{
		{
			name:         "When KubeAPIServerExternalPublicRoute is called, it should set namespace correctly",
			buildFunc:    cpomanifests.KubeAPIServerExternalPublicRoute,
			expectedName: "kube-apiserver",
		},
		{
			name:         "When KubeAPIServerExternalPrivateRoute is called, it should set namespace correctly",
			buildFunc:    cpomanifests.KubeAPIServerExternalPrivateRoute,
			expectedName: "kube-apiserver-private",
		},
		{
			name:         "When KubeAPIServerInternalRoute is called, it should set namespace correctly",
			buildFunc:    KubeAPIServerInternalRoute,
			expectedName: "kube-apiserver-internal",
		},
		{
			name:         "When OauthServerExternalPublicRoute is called, it should set namespace correctly",
			buildFunc:    cpomanifests.OauthServerExternalPublicRoute,
			expectedName: "oauth",
		},
		{
			name:         "When OauthServerExternalPrivateRoute is called, it should set namespace correctly",
			buildFunc:    cpomanifests.OauthServerExternalPrivateRoute,
			expectedName: "oauth-private",
		},
		{
			name:         "When OauthServerInternalRoute is called, it should set namespace correctly",
			buildFunc:    OauthServerInternalRoute,
			expectedName: "oauth-internal",
		},
		{
			name:         "When KonnectivityServerRoute is called, it should set namespace correctly",
			buildFunc:    KonnectivityServerRoute,
			expectedName: "konnectivity-server",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			namespace := "test-namespace"
			route := tc.buildFunc(namespace)

			g.Expect(route.Namespace).To(Equal(namespace))
			g.Expect(route.Name).To(Equal(tc.expectedName))
		})
	}
}
