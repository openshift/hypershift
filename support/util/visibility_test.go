package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type visibilityCase struct {
	name         string
	platformType hyperv1.PlatformType
	awsAccess    *hyperv1.AWSEndpointAccessType
	gcpAccess    *hyperv1.GCPEndpointAccessType
	wantPrivate  bool
	wantPublic   bool
	setupEnv     func(t *testing.T)
	annotations  map[string]string
}

func awsAccess(access hyperv1.AWSEndpointAccessType) *hyperv1.AWSEndpointAccessType {
	return &access
}

func gcpAccess(access hyperv1.GCPEndpointAccessType) *hyperv1.GCPEndpointAccessType {
	return &access
}

func baseVisibilityCases() []visibilityCase {
	return []visibilityCase{
		{
			name:         "When AWS endpoint is public it should be public and not private",
			platformType: hyperv1.AWSPlatform,
			awsAccess:    awsAccess(hyperv1.Public),
			wantPrivate:  false,
			wantPublic:   true,
		},
		{
			name:         "When AWS endpoint is public and private it should be public and private",
			platformType: hyperv1.AWSPlatform,
			awsAccess:    awsAccess(hyperv1.PublicAndPrivate),
			wantPrivate:  true,
			wantPublic:   true,
		},
		{
			name:         "When AWS endpoint is private it should be private and not public",
			platformType: hyperv1.AWSPlatform,
			awsAccess:    awsAccess(hyperv1.Private),
			wantPrivate:  true,
			wantPublic:   false,
		},
		{
			name:         "When GCP endpoint is private it should be private and not public",
			platformType: hyperv1.GCPPlatform,
			gcpAccess:    gcpAccess(hyperv1.GCPEndpointAccessPrivate),
			wantPrivate:  true,
			wantPublic:   false,
		},
		{
			name:         "When GCP endpoint is public and private it should be public and private",
			platformType: hyperv1.GCPPlatform,
			gcpAccess:    gcpAccess(hyperv1.GCPEndpointAccessPublicAndPrivate),
			wantPrivate:  true,
			wantPublic:   true,
		},
		{
			name:         "When is ARO with no Swift annotation (CI) it should not be private",
			platformType: hyperv1.NonePlatform,
			setupEnv: func(t *testing.T) {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			},
			wantPrivate: false,
			wantPublic:  true,
		},
		{
			name:         "When is ARO with Swift it should be public and private",
			platformType: hyperv1.NonePlatform,
			setupEnv: func(t *testing.T) {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			},
			annotations: map[string]string{
				hyperv1.SwiftPodNetworkInstanceAnnotation: "test-swift-instance",
			},
			wantPrivate: true,
			wantPublic:  true,
		},
	}
}

func platformSpecFromCase(tc visibilityCase) hyperv1.PlatformSpec {
	spec := hyperv1.PlatformSpec{
		Type: tc.platformType,
	}
	if tc.awsAccess != nil {
		spec.AWS = &hyperv1.AWSPlatformSpec{
			EndpointAccess: *tc.awsAccess,
		}
	}
	if tc.gcpAccess != nil {
		spec.GCP = &hyperv1.GCPPlatformSpec{
			EndpointAccess: *tc.gcpAccess,
		}
	}
	return spec
}

func hcpFromCase(tc visibilityCase) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: tc.annotations,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: platformSpecFromCase(tc),
		},
	}
}

func hcFromCase(tc visibilityCase) *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: tc.annotations,
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: platformSpecFromCase(tc),
		},
	}
}

func TestIsPrivateHCP(t *testing.T) {
	for _, tc := range baseVisibilityCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}
			if got := IsPrivateHCP(hcpFromCase(tc)); got != tc.wantPrivate {
				t.Errorf("IsPrivateHCP() = %v, want %v", got, tc.wantPrivate)
			}
		})
	}
}

func TestIsPublicHCP(t *testing.T) {
	for _, tc := range baseVisibilityCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}
			if got := IsPublicHCP(hcpFromCase(tc)); got != tc.wantPublic {
				t.Errorf("IsPublicHCP() = %v, want %v", got, tc.wantPublic)
			}
		})
	}
}

func TestIsPrivateHC(t *testing.T) {
	for _, tc := range baseVisibilityCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}
			if got := IsPrivateHC(hcFromCase(tc)); got != tc.wantPrivate {
				t.Errorf("IsPrivateHC() = %v, want %v", got, tc.wantPrivate)
			}
		})
	}
}

func TestIsPublicHC(t *testing.T) {
	for _, tc := range baseVisibilityCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}
			if got := IsPublicHC(hcFromCase(tc)); got != tc.wantPublic {
				t.Errorf("IsPublicHC() = %v, want %v", got, tc.wantPublic)
			}
		})
	}
}
