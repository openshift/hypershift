package configuration

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestReconcileClusterVersion(t *testing.T) {
	t.Parallel()
	t.Run("When the cluster version is absent, it should create owned fields and preserve error identity", func(t *testing.T) {
		assert := NewWithT(t)
		guest := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		hcp := &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{ClusterID: "cluster-id", Channel: "stable-4.22", UpdateService: "https://updates.openshift.com/graph"}}
		createOrUpdate := upsert.CreateOrUpdateFN(controllerutil.CreateOrUpdate)
		assert.Expect(reconcileClusterVersion(t.Context(), guest, createOrUpdate, ReconcileParams{ClusterID: hcp.Spec.ClusterID, Channel: hcp.Spec.Channel, UpdateService: hcp.Spec.UpdateService})).To(Succeed())
		actual := &configv1.ClusterVersion{}
		assert.Expect(guest.Get(t.Context(), client.ObjectKey{Name: "version"}, actual)).To(Succeed())
		assert.Expect(actual.Spec.ClusterID).To(Equal(configv1.ClusterID(hcp.Spec.ClusterID)))
		assert.Expect(actual.Spec.Channel).To(Equal(hcp.Spec.Channel))
		assert.Expect(actual.Spec.Upstream).To(Equal(hcp.Spec.UpdateService))
		assert.Expect(actual.Spec.Capabilities.BaselineCapabilitySet).To(Equal(configv1.ClusterVersionCapabilitySetNone))
		failure := errors.New("version failure")
		createOrUpdate = func(context.Context, client.Client, client.Object, controllerutil.MutateFn) (controllerutil.OperationResult, error) {
			return controllerutil.OperationResultNone, failure
		}
		err := reconcileClusterVersion(t.Context(), guest, createOrUpdate, ReconcileParams{ClusterID: hcp.Spec.ClusterID, Channel: hcp.Spec.Channel, UpdateService: hcp.Spec.UpdateService})
		assert.Expect(err).To(MatchError("failed to reconcile clusterVersion: version failure"))
		assert.Expect(errors.Is(err, failure)).To(BeTrue())
		assert.Expect(utilerrors.NewAggregate([]error{err}).Errors()).To(HaveLen(1))
	})
	t.Run("When known capabilities are empty, it should retain desired capabilities and reconcile only owned fields", func(t *testing.T) {
		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				ClusterID: "test-cluster-id",
			},
		}
		testOverrides := []configv1.ComponentOverride{
			{
				Kind:      "Pod",
				Group:     "",
				Name:      "test",
				Namespace: "default",
				Unmanaged: true,
			},
		}
		clusterVersion := &configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: "version",
			},
			Spec: configv1.ClusterVersionSpec{
				ClusterID: "some-other-id",
				Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
					AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
						"foo",
						"bar",
					},
				},
				Channel: "fast",
				DesiredUpdate: &configv1.Update{
					Version: "4.12.5",
					Image:   "example.com/imagens/image:latest",
					Force:   true,
				},
				Upstream:  configv1.URL("https://upstream.example.com"),
				Overrides: testOverrides,
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(clusterVersion).Build()
		g := NewWithT(t)
		err := reconcileClusterVersion(t.Context(), fakeClient, controllerutil.CreateOrUpdate, ReconcileParams{ClusterID: hcp.Spec.ClusterID, UpdateService: hcp.Spec.UpdateService, Channel: hcp.Spec.Channel, Capabilities: hcp.Spec.Capabilities})
		g.Expect(err).ToNot(HaveOccurred())
		err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(clusterVersion), clusterVersion)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(clusterVersion.Spec.ClusterID).To(Equal(configv1.ClusterID("test-cluster-id")))
		expectedCapabilities := &configv1.ClusterVersionCapabilitiesSpec{
			BaselineCapabilitySet: configv1.ClusterVersionCapabilitySetNone,
			AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityCSISnapshot,
				configv1.ClusterVersionCapabilityCloudControllerManager,
				configv1.ClusterVersionCapabilityCloudCredential,
				configv1.ClusterVersionCapabilityConsole,
				configv1.ClusterVersionCapabilityDeploymentConfig,
				configv1.ClusterVersionCapabilityImageRegistry,
				configv1.ClusterVersionCapabilityIngress,
				configv1.ClusterVersionCapabilityInsights,
				configv1.ClusterVersionCapabilityMachineAPI,
				configv1.ClusterVersionCapabilityNodeTuning,
				configv1.ClusterVersionCapabilityOperatorLifecycleManager,
				configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
				configv1.ClusterVersionCapabilityStorage,
				configv1.ClusterVersionCapabilityMarketplace,
				configv1.ClusterVersionCapabilityOpenShiftSamples,
			},
		}
		g.Expect(clusterVersion.Spec.Capabilities).To(Equal(expectedCapabilities))
		g.Expect(clusterVersion.Spec.DesiredUpdate).To(BeNil())
		g.Expect(clusterVersion.Spec.Overrides).To(Equal(testOverrides))
		g.Expect(clusterVersion.Spec.Channel).To(BeEmpty())
	})
}

func TestReconcileClusterVersionWithDisabledCapabilities(t *testing.T) {
	t.Parallel()
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID: "test-cluster-id",
			Capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{
					hyperv1.ImageRegistryCapability, hyperv1.OpenShiftSamplesCapability, hyperv1.InsightsCapability, hyperv1.ConsoleCapability, hyperv1.NodeTuningCapability, hyperv1.IngressCapability,
				},
			},
		},
	}
	testOverrides := []configv1.ComponentOverride{
		{
			Kind:      "Pod",
			Group:     "",
			Name:      "test",
			Namespace: "default",
			Unmanaged: true,
		},
	}
	clusterVersion := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: "some-other-id",
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
					"foo",
					"bar",
				},
			},
			Channel: "fast",
			DesiredUpdate: &configv1.Update{
				Version: "4.12.5",
				Image:   "example.com/imagens/image:latest",
				Force:   true,
			},
			Upstream:  configv1.URL("https://upstream.example.com"),
			Overrides: testOverrides,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(clusterVersion).Build()
	g := NewWithT(t)
	err := reconcileClusterVersion(t.Context(), fakeClient, controllerutil.CreateOrUpdate, ReconcileParams{ClusterID: hcp.Spec.ClusterID, UpdateService: hcp.Spec.UpdateService, Channel: hcp.Spec.Channel, Capabilities: hcp.Spec.Capabilities})
	g.Expect(err).ToNot(HaveOccurred())
	err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(clusterVersion), clusterVersion)
	g.Expect(err).ToNot(HaveOccurred())

	expectedCapabilities := &configv1.ClusterVersionCapabilitiesSpec{
		BaselineCapabilitySet: configv1.ClusterVersionCapabilitySetNone,
		AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
			configv1.ClusterVersionCapabilityBuild,
			configv1.ClusterVersionCapabilityCSISnapshot,
			configv1.ClusterVersionCapabilityCloudControllerManager,
			configv1.ClusterVersionCapabilityCloudCredential,
			//configv1.ClusterVersionCapabilityConsole,
			configv1.ClusterVersionCapabilityDeploymentConfig,
			// configv1.ClusterVersionCapabilityImageRegistry,
			//configv1.ClusterVersionCapabilityIngress,
			//configv1.ClusterVersionCapabilityInsights,
			configv1.ClusterVersionCapabilityMachineAPI,
			//configv1.ClusterVersionCapabilityNodeTuning,
			configv1.ClusterVersionCapabilityOperatorLifecycleManager,
			configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
			configv1.ClusterVersionCapabilityStorage,
			configv1.ClusterVersionCapabilityMarketplace,
			// configv1.ClusterVersionCapabilityOpenShiftSamples,
		},
	}
	g.Expect(clusterVersion.Spec.Capabilities).To(Equal(expectedCapabilities))
}

func TestReconcileClusterVersionWithEnabledCapabilities(t *testing.T) {
	t.Parallel()
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID: "test-cluster-id",
			Capabilities: &hyperv1.Capabilities{
				Enabled: []hyperv1.OptionalCapability{
					hyperv1.BaremetalCapability,
				},
			},
		},
	}
	testOverrides := []configv1.ComponentOverride{
		{
			Kind:      "Pod",
			Group:     "",
			Name:      "test",
			Namespace: "default",
			Unmanaged: true,
		},
	}
	clusterVersion := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: "some-other-id",
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
					"foo",
					"bar",
				},
			},
			Channel: "fast",
			DesiredUpdate: &configv1.Update{
				Version: "4.12.5",
				Image:   "example.com/imagens/image:latest",
				Force:   true,
			},
			Upstream:  configv1.URL("https://upstream.example.com"),
			Overrides: testOverrides,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(clusterVersion).Build()
	g := NewWithT(t)
	err := reconcileClusterVersion(t.Context(), fakeClient, controllerutil.CreateOrUpdate, ReconcileParams{ClusterID: hcp.Spec.ClusterID, UpdateService: hcp.Spec.UpdateService, Channel: hcp.Spec.Channel, Capabilities: hcp.Spec.Capabilities})
	g.Expect(err).ToNot(HaveOccurred())
	err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(clusterVersion), clusterVersion)
	g.Expect(err).ToNot(HaveOccurred())

	expectedCapabilities := &configv1.ClusterVersionCapabilitiesSpec{
		BaselineCapabilitySet: configv1.ClusterVersionCapabilitySetNone,
		AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
			configv1.ClusterVersionCapabilityBuild,
			configv1.ClusterVersionCapabilityCSISnapshot,
			configv1.ClusterVersionCapabilityCloudControllerManager,
			configv1.ClusterVersionCapabilityCloudCredential,
			configv1.ClusterVersionCapabilityConsole,
			configv1.ClusterVersionCapabilityDeploymentConfig,
			configv1.ClusterVersionCapabilityImageRegistry,
			configv1.ClusterVersionCapabilityIngress,
			configv1.ClusterVersionCapabilityInsights,
			configv1.ClusterVersionCapabilityMachineAPI,
			configv1.ClusterVersionCapabilityNodeTuning,
			configv1.ClusterVersionCapabilityOperatorLifecycleManager,
			configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
			configv1.ClusterVersionCapabilityStorage,
			configv1.ClusterVersionCapabilityBaremetal,
			configv1.ClusterVersionCapabilityMarketplace,
			configv1.ClusterVersionCapabilityOpenShiftSamples,
		},
	}
	g.Expect(clusterVersion.Spec.Capabilities).To(Equal(expectedCapabilities))
}

func TestReconcileClusterVersionWhenGuestCVOHasOlderCapabilities(t *testing.T) {
	t.Parallel()
	t.Run("When nonempty known capabilities exclude Console, it should remove that default-enabled capability", func(t *testing.T) {
		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				ClusterID: "test-cluster-id",
			},
		}
		olderCVOKnownCaps := []configv1.ClusterVersionCapability{
			configv1.ClusterVersionCapabilityBuild,
			configv1.ClusterVersionCapabilityCSISnapshot,
			configv1.ClusterVersionCapabilityCloudControllerManager,
			configv1.ClusterVersionCapabilityCloudCredential,
			configv1.ClusterVersionCapabilityDeploymentConfig,
			configv1.ClusterVersionCapabilityImageRegistry,
			configv1.ClusterVersionCapabilityIngress,
			configv1.ClusterVersionCapabilityInsights,
			configv1.ClusterVersionCapabilityMachineAPI,
			configv1.ClusterVersionCapabilityNodeTuning,
			configv1.ClusterVersionCapabilityOperatorLifecycleManager,
			configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
			configv1.ClusterVersionCapabilityStorage,
			configv1.ClusterVersionCapabilityBaremetal,
			configv1.ClusterVersionCapabilityMarketplace,
			configv1.ClusterVersionCapabilityOpenShiftSamples,
		}
		clusterVersion := &configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: "version",
			},
			Status: configv1.ClusterVersionStatus{
				Capabilities: configv1.ClusterVersionCapabilitiesStatus{
					KnownCapabilities: olderCVOKnownCaps,
				},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(clusterVersion).Build()
		g := NewWithT(t)
		err := reconcileClusterVersion(t.Context(), fakeClient, controllerutil.CreateOrUpdate, ReconcileParams{ClusterID: hcp.Spec.ClusterID, UpdateService: hcp.Spec.UpdateService, Channel: hcp.Spec.Channel, Capabilities: hcp.Spec.Capabilities})
		g.Expect(err).ToNot(HaveOccurred())
		err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(clusterVersion), clusterVersion)
		g.Expect(err).ToNot(HaveOccurred())

		// ClusterAPI and CompatibilityRequirements should be filtered out
		expectedCapabilities := &configv1.ClusterVersionCapabilitiesSpec{
			BaselineCapabilitySet: configv1.ClusterVersionCapabilitySetNone,
			AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapabilityBuild,
				configv1.ClusterVersionCapabilityCSISnapshot,
				configv1.ClusterVersionCapabilityCloudControllerManager,
				configv1.ClusterVersionCapabilityCloudCredential,
				// ClusterAPI filtered out - not in knownCapabilities
				// CompatibilityRequirements filtered out - not in knownCapabilities
				configv1.ClusterVersionCapabilityDeploymentConfig,
				configv1.ClusterVersionCapabilityImageRegistry,
				configv1.ClusterVersionCapabilityIngress,
				configv1.ClusterVersionCapabilityInsights,
				configv1.ClusterVersionCapabilityMachineAPI,
				configv1.ClusterVersionCapabilityNodeTuning,
				configv1.ClusterVersionCapabilityOperatorLifecycleManager,
				configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
				configv1.ClusterVersionCapabilityStorage,
				configv1.ClusterVersionCapabilityMarketplace,
				configv1.ClusterVersionCapabilityOpenShiftSamples,
			},
		}
		g.Expect(clusterVersion.Spec.Capabilities).To(Equal(expectedCapabilities))
	})
}
