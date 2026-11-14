package hostedcontrolplane

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/support/api"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	testingclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type azureLocationTestCredential struct{}

func (azureLocationTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, errors.New("unexpected token request")
}

type azureLocationTestClient struct {
	calls     [3]atomic.Int32
	failAt    int
	err       error
	locations [3]string
	beforeGet func()
}

func newAzureLocationTestReconciler() (*HostedControlPlaneReconciler, *hyperv1.HostedControlPlane, *azureLocationTestClient) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: "clusters-test", UID: "hcp-1", Generation: 1, Finalizers: []string{finalizer}},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64",
			Platform: hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform, Azure: &hyperv1.AzurePlatformSpec{
				Cloud: "AzurePublicCloud", SubscriptionID: "00000000-0000-0000-0000-000000000001",
				VnetID:            "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/network/providers/Microsoft.Network/virtualNetworks/hcp-vnet",
				SecurityGroupID:   "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/network/providers/Microsoft.Network/networkSecurityGroups/hcp-nsg",
				ResourceGroupName: "hcp-managed",
			}},
		},
	}
	fixture := &azureLocationTestClient{failAt: -1, locations: [3]string{"eastus", "eastus", "eastus"}}
	probe := func(stage int) error {
		fixture.calls[stage].Add(1)
		if fixture.beforeGet != nil {
			fixture.beforeGet()
		}
		if fixture.failAt == stage {
			return fixture.err
		}
		return nil
	}
	r := &HostedControlPlaneReconciler{
		clock:                         testingclock.NewFakeClock(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)),
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportNoCapabilities{},
		Client:                        fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
		azureLocationClient: &azureLocationClient{
			vnet: func(context.Context, string, string, azcore.TokenCredential, string) (armnetwork.VirtualNetworksClientGetResponse, error) {
				return armnetwork.VirtualNetworksClientGetResponse{VirtualNetwork: armnetwork.VirtualNetwork{Location: ptr.To(fixture.locations[0])}}, probe(0)
			},
			securityGroup: func(context.Context, string, string, azcore.TokenCredential, string) (armnetwork.SecurityGroupsClientGetResponse, error) {
				return armnetwork.SecurityGroupsClientGetResponse{SecurityGroup: armnetwork.SecurityGroup{Location: ptr.To(fixture.locations[1])}}, probe(1)
			},
			resourceGroup: func(context.Context, string, string, azcore.TokenCredential, string) (armresources.ResourceGroupsClientGetResponse, error) {
				return armresources.ResourceGroupsClientGetResponse{ResourceGroup: armresources.ResourceGroup{Location: ptr.To(fixture.locations[2])}}, probe(2)
			},
		},
	}
	r.cpoAzureCredentialsLoaded.Store(hcp.Namespace+cpoAzureCredentials, azureLocationTestCredential{})
	return r, hcp, fixture
}

func (fixture *azureLocationTestClient) counts() [3]int32 {
	return [3]int32{fixture.calls[0].Load(), fixture.calls[1].Load(), fixture.calls[2].Load()}
}

func azureLocationTestError(status int, header string) error {
	return fmt.Errorf("Azure GET: %w", &azcore.ResponseError{
		StatusCode:  status,
		RawResponse: &http.Response{Header: http.Header{"Retry-After": {header}}},
	})
}

func setAzureLocationTestCondition(hcp *hyperv1.HostedControlPlane, status metav1.ConditionStatus, generation int64) {
	meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type: string(hyperv1.ValidHostedControlPlaneConfiguration), Status: status,
		ObservedGeneration: generation, Reason: hyperv1.AsExpectedReason,
	})
}

func TestVerifyResourceGroupLocationsMatch(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		status     metav1.ConditionStatus
		generation int64
		wantCalls  [3]int32
	}{
		{name: "When validation has never run, it should fetch all locations", wantCalls: [3]int32{1, 1, 1}},
		{name: "When validation succeeded for this generation, it should reuse its proof", status: metav1.ConditionTrue, generation: 1},
		{name: "When validation succeeded for an older generation, it should revalidate", status: metav1.ConditionTrue, generation: 0, wantCalls: [3]int32{1, 1, 1}},
		{name: "When validation is false, it should revalidate", status: metav1.ConditionFalse, generation: 1, wantCalls: [3]int32{1, 1, 1}},
		{name: "When validation is unknown, it should revalidate", status: metav1.ConditionUnknown, generation: 1, wantCalls: [3]int32{1, 1, 1}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			r, hcp, fixture := newAzureLocationTestReconciler()
			if testCase.status != "" {
				setAzureLocationTestCondition(hcp, testCase.status, testCase.generation)
			}
			if err := r.verifyResourceGroupLocationsMatch(t.Context(), hcp); err != nil {
				t.Fatal(err)
			}
			if got := fixture.counts(); got != testCase.wantCalls {
				t.Fatalf("GET counts = %v, want %v", got, testCase.wantCalls)
			}
			if len(r.azureLocationValidation.entries) != 0 {
				t.Fatal("successful validation retained cooldown state")
			}
		})
	}
	t.Run("When location lookup is throttled, it should suppress early calls and recover at the deadline", func(t *testing.T) {
		for stage := range 3 {
			r, hcp, fixture := newAzureLocationTestReconciler()
			fixture.failAt, fixture.err = stage, azureLocationTestError(http.StatusTooManyRequests, "120")
			err := r.verifyResourceGroupLocationsMatch(t.Context(), hcp)
			var responseErr *azcore.ResponseError
			if !errors.As(err, &responseErr) || r.azureLocationRetryDelay(err) != 2*time.Minute {
				t.Fatalf("stage %d: missing wrapped throttle or delay: %v", stage, err)
			}
			firstCounts := fixture.counts()
			for index, count := range firstCounts {
				var expected int32
				if index <= stage {
					expected = 1
				}
				if count != expected {
					t.Fatalf("partial GET counts = %v", firstCounts)
				}
			}
			r.clock.(*testingclock.FakeClock).Step(time.Minute)
			if err := r.verifyResourceGroupLocationsMatch(t.Context(), hcp); r.azureLocationRetryDelay(err) != time.Minute || fixture.counts() != firstCounts {
				t.Fatalf("stage %d: early retry was not suppressed: %v", stage, err)
			}
			r.clock.(*testingclock.FakeClock).Step(time.Minute)
			fixture.failAt = -1
			if err := r.verifyResourceGroupLocationsMatch(t.Context(), hcp); err != nil {
				t.Fatal(err)
			}
			for index, count := range fixture.counts() {
				if count != firstCounts[index]+1 {
					t.Fatalf("recovery GET counts = %v", fixture.counts())
				}
			}
			if len(r.azureLocationValidation.entries) != 0 {
				t.Fatal("recovered validation retained cooldown")
			}
		}
	})
	t.Run("When UID or generation changes, it should discard the previous cooldown", func(t *testing.T) {
		for _, recreate := range []bool{false, true} {
			r, hcp, fixture := newAzureLocationTestReconciler()
			fixture.failAt, fixture.err = 0, azureLocationTestError(http.StatusTooManyRequests, "120")
			_ = r.verifyResourceGroupLocationsMatch(t.Context(), hcp)
			if recreate {
				hcp.UID = "hcp-2"
			} else {
				hcp.Generation++
			}
			fixture.failAt = -1
			if err := r.verifyResourceGroupLocationsMatch(t.Context(), hcp); err != nil || fixture.counts() != [3]int32{2, 1, 1} {
				t.Fatalf("new identity did not revalidate: %v, %v", err, fixture.counts())
			}
		}
	})
	t.Run("When concurrent events see a throttle, it should share the cooldown safely", func(t *testing.T) {
		r, hcp, fixture := newAzureLocationTestReconciler()
		fixture.failAt, fixture.err = 0, azureLocationTestError(http.StatusTooManyRequests, "120")
		var workers sync.WaitGroup
		for range 20 {
			workers.Go(func() {
				if err := r.verifyResourceGroupLocationsMatch(t.Context(), hcp); r.azureLocationRetryDelay(err) != 2*time.Minute {
					t.Errorf("missing concurrent throttle: %v", err)
				}
			})
		}
		workers.Wait()
		if fixture.counts() != [3]int32{1, 0, 0} {
			t.Fatalf("concurrent GET counts = %v", fixture.counts())
		}
	})
	t.Run("When locations mismatch, it should fail without scheduling a retry", func(t *testing.T) {
		r, hcp, fixture := newAzureLocationTestReconciler()
		fixture.locations[1] = "westus"
		err := r.verifyResourceGroupLocationsMatch(t.Context(), hcp)
		if err == nil || !strings.Contains(err.Error(), "locations of the resource groups do not match") || r.azureLocationRetryDelay(err) != 0 {
			t.Fatalf("unexpected mismatch result: %v", err)
		}
	})
}

func TestValidateConfigAndClusterCapabilities(t *testing.T) {
	t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
	for _, testCase := range []struct {
		name     string
		prepare  func(*HostedControlPlaneReconciler, *hyperv1.HostedControlPlane)
		wantErr  string
		wantGets [3]int32
	}{
		{name: "When Azure validation succeeds, it should validate all three resources", wantGets: [3]int32{1, 1, 1}},
		{name: "When Azure success is cached, it should skip only location requests", prepare: func(_ *HostedControlPlaneReconciler, hcp *hyperv1.HostedControlPlane) {
			setAzureLocationTestCondition(hcp, metav1.ConditionTrue, hcp.Generation)
		}},
		{name: "When Routes are unsupported despite cached success, it should reject the configuration", prepare: func(_ *HostedControlPlaneReconciler, hcp *hyperv1.HostedControlPlane) {
			setAzureLocationTestCondition(hcp, metav1.ConditionTrue, hcp.Generation)
			hcp.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}}}
		}, wantErr: "does not support Routes"},
		{name: "When authentication is invalid despite cached success, it should reject authentication", prepare: func(_ *HostedControlPlaneReconciler, hcp *hyperv1.HostedControlPlane) {
			setAzureLocationTestCondition(hcp, metav1.ConditionTrue, hcp.Generation)
			hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{Type: "invalid"}}
		}, wantErr: "unknown type"},
		{name: "When authentication is invalid without cached success, it should check Azure first", prepare: func(_ *HostedControlPlaneReconciler, hcp *hyperv1.HostedControlPlane) {
			hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{Type: "invalid"}}
		}, wantErr: "unknown type", wantGets: [3]int32{1, 1, 1}},
		{name: "When a non-Azure platform is used, it should not fetch Azure resources", prepare: func(_ *HostedControlPlaneReconciler, hcp *hyperv1.HostedControlPlane) {
			hcp.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform}
		}},
		{name: "When Routes are supported, it should continue validating Azure", prepare: func(r *HostedControlPlaneReconciler, hcp *hyperv1.HostedControlPlane) {
			r.ManagementClusterCapabilities = &fakecapabilities.FakeSupportAllCapabilities{}
			hcp.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}}}
		}, wantGets: [3]int32{1, 1, 1}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			r, hcp, fixture := newAzureLocationTestReconciler()
			if testCase.prepare != nil {
				testCase.prepare(r, hcp)
			}
			err := r.validateConfigAndClusterCapabilities(t.Context(), hcp)
			if testCase.wantErr == "" && err != nil || testCase.wantErr != "" && (err == nil || !strings.Contains(err.Error(), testCase.wantErr)) {
				t.Fatalf("validation error = %v, want %q", err, testCase.wantErr)
			}
			if fixture.counts() != testCase.wantGets {
				t.Fatalf("GET counts = %v, want %v", fixture.counts(), testCase.wantGets)
			}
		})
	}
	t.Run("When Azure is not managed ARO, it should not validate locations", func(t *testing.T) {
		t.Setenv("MANAGED_SERVICE", "")
		r, hcp, fixture := newAzureLocationTestReconciler()
		if err := r.validateConfigAndClusterCapabilities(t.Context(), hcp); err != nil || fixture.counts() != [3]int32{} {
			t.Fatalf("non-ARO validation = %v, calls %v", err, fixture.counts())
		}
	})
	t.Run("When a referenced authentication config changes, it should recheck it despite cached Azure success", func(t *testing.T) {
		r, hcp, fixture := newAzureLocationTestReconciler()
		setAzureLocationTestCondition(hcp, metav1.ConditionTrue, hcp.Generation)
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{
			Type: configv1.AuthenticationTypeOIDC,
			OIDCProviders: []configv1.OIDCProvider{{Name: "issuer", Issuer: configv1.TokenIssuer{
				URL: "https://issuer.redhat.com", CertificateAuthority: configv1.ConfigMapNameReference{Name: "issuer-ca"},
			}}},
		}}
		firstErr := r.validateConfigAndClusterCapabilities(t.Context(), hcp)
		if firstErr == nil || !strings.Contains(firstErr.Error(), "not found") {
			t.Fatalf("missing CA result = %v", firstErr)
		}
		if err := r.Create(t.Context(), &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "issuer-ca", Namespace: hcp.Namespace}}); err != nil {
			t.Fatal(err)
		}
		secondErr := r.validateConfigAndClusterCapabilities(t.Context(), hcp)
		if secondErr == nil || !strings.Contains(secondErr.Error(), "does not contain key") || fixture.counts() != [3]int32{} || hcp.Generation != 1 {
			t.Fatalf("changed CA was not reread: %v, counts %v", secondErr, fixture.counts())
		}
	})
	t.Run("When a spec change fails Routes validation, it should still invalidate old cooldown state", func(t *testing.T) {
		r, hcp, fixture := newAzureLocationTestReconciler()
		fixture.failAt, fixture.err = 0, azureLocationTestError(429, "120")
		_ = r.validateConfigAndClusterCapabilities(t.Context(), hcp)
		hcp.Generation++
		hcp.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}}}
		if err := r.validateConfigAndClusterCapabilities(t.Context(), hcp); err == nil || len(r.azureLocationValidation.entries) != 0 || fixture.counts() != [3]int32{1, 0, 0} {
			t.Fatalf("stale cooldown survived invalid Routes: %v", err)
		}
	})
}

func newAzureLocationReconcileTest(t *testing.T) (*HostedControlPlaneReconciler, *hyperv1.HostedControlPlane, *azureLocationTestClient, *atomic.Int32) {
	t.Helper()
	r, hcp, fixture := newAzureLocationTestReconciler()
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: hcp.Namespace}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("{}")}}
	r.Client = fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hcp).WithObjects(hcp, pullSecret).Build()
	r.ReleaseProvider = &releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator{Delegate: &fakereleaseprovider.FakeReleaseProvider{Version: "4.21.10"}}
	r.ImageMetadataProvider = &fakeVersionImageMetadataProvider{fakeRef: &reference.DockerImageReference{Registry: "quay.io", Namespace: "openshift-release-dev", Name: "ocp-release", ID: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}}
	r.reconcileInfrastructureStatus = func(context.Context, *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error) {
		return infra.InfrastructureStatus{}, nil
	}
	updates := &atomic.Int32{}
	r.createOrUpdate = func(*hyperv1.HostedControlPlane) upsert.CreateOrUpdateFN {
		updates.Add(1)
		return func(context.Context, client.Client, client.Object, controllerutil.MutateFn) (controllerutil.OperationResult, error) {
			return controllerutil.OperationResultNone, errors.New("test stops after entering control plane update")
		}
	}
	return r, hcp, fixture, updates
}

func TestReconcile(t *testing.T) {
	t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
	t.Run("When a final location GET is throttled, it should persist false and recover on its scheduled retry", testReconcileAzureLocationRecovery)
	t.Run("When an error is not 429, it should persist false without a timed retry", testReconcileAzureLocationPermanentError)
	t.Run("When status persistence conflicts, it should not schedule a retry from unpersisted validation", testReconcileAzureLocationStatusConflict)
	t.Run("When the HCP is not found, it should clear its cooldown", testReconcileAzureLocationNotFound)
	t.Run("When the HCP is deleted, it should clear its cooldown before deletion reconciliation", testReconcileAzureLocationDeletion)
	t.Run("When spec changes during a location GET, it should not persist the old generation result", testReconcileAzureLocationGenerationConflict)
	t.Run("When a final 429 has Retry-After, it should safely schedule that delay or its fallback", testReconcileAzureLocationRetryAfter)
}

func testReconcileAzureLocationRecovery(t *testing.T) {
	for stage := range 3 {
		r, hcp, fixture, updates := newAzureLocationReconcileTest(t)
		fixture.failAt, fixture.err = stage, azureLocationTestError(http.StatusTooManyRequests, "120")
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hcp)}
		result, err := r.Reconcile(t.Context(), request)
		if err != nil || result.RequeueAfter != 2*time.Minute || updates.Load() != 0 {
			t.Fatalf("throttled reconcile = %v, %v, updates %d", result, err, updates.Load())
		}
		assertAzureLocationCondition(t, r, hcp, metav1.ConditionFalse)
		firstCounts := fixture.counts()
		r.clock.(*testingclock.FakeClock).Step(time.Minute)
		result, err = r.Reconcile(t.Context(), request)
		if err != nil || result.RequeueAfter != time.Minute || fixture.counts() != firstCounts || updates.Load() != 0 {
			t.Fatalf("early reconcile = %v, %v, calls %v", result, err, fixture.counts())
		}
		fixture.failAt = -1
		r.clock.(*testingclock.FakeClock).Step(time.Minute)
		_, _ = r.Reconcile(t.Context(), request)
		assertAzureLocationCondition(t, r, hcp, metav1.ConditionTrue)
		if updates.Load() != 1 {
			t.Fatal("recovery never reached control plane update")
		}
		secondCounts := fixture.counts()
		_, _ = r.Reconcile(t.Context(), request)
		if fixture.counts() != secondCounts || updates.Load() != 2 {
			t.Fatalf("successful proof did not cache GETs: %v -> %v", secondCounts, fixture.counts())
		}
	}
}

func testReconcileAzureLocationPermanentError(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 500} {
		r, hcp, fixture, updates := newAzureLocationReconcileTest(t)
		fixture.failAt, fixture.err = 0, azureLocationTestError(status, "120")
		result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
		if err != nil || !result.IsZero() || updates.Load() != 0 || len(r.azureLocationValidation.entries) != 0 {
			t.Fatalf("HTTP %d: result %v, error %v", status, result, err)
		}
		assertAzureLocationCondition(t, r, hcp, metav1.ConditionFalse)
	}
}

func testReconcileAzureLocationStatusConflict(t *testing.T) {
	r, hcp, fixture, updates := newAzureLocationReconcileTest(t)
	fixture.failAt, fixture.err = 0, azureLocationTestError(429, "120")
	underlying := r.Client
	r.Client = interceptor.NewClient(underlying.(client.WithWatch), interceptor.Funcs{SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
		return apierrors.NewConflict(schema.GroupResource{Group: hyperv1.SchemeGroupVersion.Group, Resource: "hostedcontrolplanes"}, hcp.Name, errors.New("injected status conflict"))
	}})
	result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
	if !apierrors.IsConflict(err) || !result.IsZero() || updates.Load() != 0 {
		t.Fatalf("conflicting reconcile = %v, %v", result, err)
	}
	persisted := &hyperv1.HostedControlPlane{}
	if err := underlying.Get(t.Context(), client.ObjectKeyFromObject(hcp), persisted); err != nil {
		t.Fatal(err)
	}
	if meta.FindStatusCondition(persisted.Status.Conditions, string(hyperv1.ValidHostedControlPlaneConfiguration)) != nil {
		t.Fatal("conflict persisted stale validation")
	}
	r.Client = underlying
	result, err = r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
	if err != nil || result.RequeueAfter != 2*time.Minute || fixture.counts() != [3]int32{1, 0, 0} {
		t.Fatalf("conflict recovery = %v, %v, counts %v", result, err, fixture.counts())
	}
	assertAzureLocationCondition(t, r, hcp, metav1.ConditionFalse)
}

func testReconcileAzureLocationNotFound(t *testing.T) {
	r, hcp, fixture := newAzureLocationTestReconciler()
	fixture.failAt, fixture.err = 0, azureLocationTestError(429, "120")
	_ = r.verifyResourceGroupLocationsMatch(t.Context(), hcp)
	result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
	if err != nil || !result.IsZero() || len(r.azureLocationValidation.entries) != 0 {
		t.Fatalf("not-found cleanup = %v, %v", result, err)
	}
}

func testReconcileAzureLocationDeletion(t *testing.T) {
	r, hcp, fixture, _ := newAzureLocationReconcileTest(t)
	fixture.failAt, fixture.err = 0, azureLocationTestError(429, "120")
	_ = r.verifyResourceGroupLocationsMatch(t.Context(), hcp)
	if err := r.Delete(t.Context(), hcp); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hcp)}); err != nil || len(r.azureLocationValidation.entries) != 0 {
		t.Fatalf("deletion cleanup = %v", err)
	}
}

func testReconcileAzureLocationGenerationConflict(t *testing.T) {
	r, hcp, fixture, updates := newAzureLocationReconcileTest(t)
	fixture.failAt, fixture.err = 0, azureLocationTestError(429, "120")
	changed := false
	fixture.beforeGet = func() {
		if changed {
			return
		}
		changed = true
		latest := &hyperv1.HostedControlPlane{}
		if err := r.Get(t.Context(), client.ObjectKeyFromObject(hcp), latest); err != nil {
			t.Fatal(err)
		}
		latest.Generation++
		latest.Spec.IssuerURL = "https://new-issuer.redhat.com"
		if err := r.Update(t.Context(), latest); err != nil {
			t.Fatal(err)
		}
	}
	result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
	if !apierrors.IsConflict(err) || !result.IsZero() || updates.Load() != 0 {
		t.Fatalf("spec-change reconcile = %v, %v", result, err)
	}
	latest := &hyperv1.HostedControlPlane{}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(hcp), latest); err != nil {
		t.Fatal(err)
	}
	if latest.Generation != 2 || meta.FindStatusCondition(latest.Status.Conditions, string(hyperv1.ValidHostedControlPlaneConfiguration)) != nil {
		t.Fatal("stale validation overwrote new spec status")
	}
	result, err = r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
	if err != nil || result.RequeueAfter != 2*time.Minute || fixture.counts() != [3]int32{2, 0, 0} {
		t.Fatalf("new generation failed to revalidate: %v, %v, %v", result, err, fixture.counts())
	}
}

func testReconcileAzureLocationRetryAfter(t *testing.T) {
	for _, testCase := range []struct {
		header     string
		futureDate bool
	}{
		{header: ""},
		{header: "not-a-duration"},
		{header: "0"},
		{header: "-1"},
		{header: "9223372036854775807"},
		{header: "Fri, 31 Dec 9999 23:59:59 GMT"},
		{header: time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)},
		{header: time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat), futureDate: true},
	} {
		r, hcp, fixture, _ := newAzureLocationReconcileTest(t)
		fixture.failAt, fixture.err = 0, azureLocationTestError(429, testCase.header)
		result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
		if err != nil {
			t.Fatal(err)
		}
		if testCase.futureDate {
			if result.RequeueAfter < time.Minute || result.RequeueAfter > 2*time.Minute {
				t.Fatalf("HTTP-date delay = %v", result.RequeueAfter)
			}
		} else if result.RequeueAfter != 5*time.Minute {
			t.Fatalf("fallback for %q = %v", testCase.header, result.RequeueAfter)
		}
	}
}

func TestAzureLocationRetryDelay(t *testing.T) {
	t.Run("When the deadline elapsed during status reconciliation, it should schedule an immediate positive retry", func(t *testing.T) {
		r, _, _ := newAzureLocationTestReconciler()
		err := &azureLocationRetryError{cause: azureLocationTestError(429, "120"), deadline: r.clock.Now().Add(-time.Second)}
		if delay := r.azureLocationRetryDelay(err); delay != time.Nanosecond {
			t.Fatalf("expired delay = %v", delay)
		}
	})
	t.Run("When a non-location Azure error is returned, it should not schedule a retry", func(t *testing.T) {
		r := &HostedControlPlaneReconciler{}
		if delay := r.azureLocationRetryDelay(azureLocationTestError(429, "120")); delay != 0 {
			t.Fatalf("unrelated error delay = %v", delay)
		}
		if delay := r.azureLocationRetryDelay(&azureLocationRetryError{cause: errors.New("test"), deadline: time.Now().Add(time.Minute)}); delay <= 0 {
			t.Fatal("real-clock fallback did not schedule retry")
		}
	})
}

func assertAzureLocationCondition(t *testing.T, r *HostedControlPlaneReconciler, hcp *hyperv1.HostedControlPlane, want metav1.ConditionStatus) {
	t.Helper()
	persisted := &hyperv1.HostedControlPlane{}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(hcp), persisted); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(persisted.Status.Conditions, string(hyperv1.ValidHostedControlPlaneConfiguration))
	if condition == nil || condition.Status != want || condition.ObservedGeneration != hcp.Generation {
		t.Fatalf("persisted validation = %#v, want %s generation %d", condition, want, hcp.Generation)
	}
	if want == metav1.ConditionFalse && condition.Reason != hyperv1.InsufficientClusterCapabilitiesReason {
		t.Fatalf("invalid reason = %s", condition.Reason)
	}
}
