package hostedcluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	crdassets "github.com/openshift/hypershift/cmd/install/assets/crds"
	supportapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"

	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capiconversion "sigs.k8s.io/cluster-api/core/webhooks/conversion"
	conversionutil "sigs.k8s.io/cluster-api/util/conversion"
	crconversion "sigs.k8s.io/controller-runtime/pkg/conversion"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"
)

func TestNewCAPIConversionRegistry(t *testing.T) {
	globalTypes := maps.Clone(supportapi.Scheme.AllKnownTypes())
	scheme := freshConversionScheme(t)
	managerTypes := maps.Clone(scheme.AllKnownTypes())
	managerVersions := scheme.VersionsForGroupKind(schema.GroupKind{Group: "ipam.cluster.x-k8s.io", Kind: "IPAddress"})
	registry, err := newCAPIConversionRegistry(scheme)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !reflect.DeepEqual(globalTypes, supportapi.Scheme.AllKnownTypes()) {
			t.Error("conversion tests mutated the package-global scheme")
		}
		if !reflect.DeepEqual(managerTypes, scheme.AllKnownTypes()) || !reflect.DeepEqual(managerVersions, scheme.VersionsForGroupKind(schema.GroupKind{Group: "ipam.cluster.x-k8s.io", Kind: "IPAddress"})) {
			t.Error("registry initialization mutated the manager scheme or IPAM version priority")
		}
	})
	handler := conversion.NewWebhookHandler(scheme, registry)
	restore := capiconversion.SwapAPIVersionGetter(capiAPIVersionGetter(scheme))
	t.Cleanup(restore)
	t.Run("When the scheme lacks CAPI types, it should return a converter creation error", func(t *testing.T) {
		registry, err := newCAPIConversionRegistry(runtime.NewScheme())
		if err == nil || registry != nil {
			t.Fatalf("expected a converter creation error and nil registry, got registry=%v error=%v", registry, err)
		}
	})
	testInstalledCAPIConversions(t, scheme, registry, handler)
	testCAPIRoundTrips(t, scheme, handler)
	testCAPIFallback(t, registry)
}

func testInstalledCAPIConversions(t *testing.T, scheme *runtime.Scheme, registry conversion.Registry, handler http.Handler) {
	t.Helper()
	getter := capiAPIVersionGetter(scheme)
	crds := crdassets.CustomResourceDefinitions("v1beta1", func(_ string, crd *apiextensionsv1.CustomResourceDefinition) bool {
		return crd.Kind == "CustomResourceDefinition"
	}, nil)
	if len(crds) == 0 {
		t.Fatal("expected installed CRDs")
	}
	for _, object := range crds {
		crd := object.(*apiextensionsv1.CustomResourceDefinition)
		groupKind := schema.GroupKind{Group: crd.Spec.Group, Kind: crd.Spec.Names.Kind}
		isCAPI := crd.Spec.Group == "cluster.x-k8s.io" || crd.Spec.Group == "ipam.cluster.x-k8s.io" || crd.Spec.Group == "addons.cluster.x-k8s.io"
		if !isCAPI && len(scheme.VersionsForGroupKind(groupKind)) == 0 {
			continue
		}
		t.Run(fmt.Sprintf("When %s is installed, it should serve the first registered reference version", groupKind), func(t *testing.T) {
			versions := scheme.VersionsForGroupKind(groupKind)
			if len(versions) == 0 {
				t.Fatalf("no registered version for installed %s", groupKind)
			}
			selectedVersion, err := getter(t.Context(), groupKind)
			if err != nil {
				t.Fatal(err)
			}
			if selectedVersion != versions[0].String() {
				t.Fatalf("getter selected %s instead of first registered version %s", selectedVersion, versions[0])
			}
			for _, version := range crd.Spec.Versions {
				if selectedVersion == (schema.GroupVersion{Group: crd.Spec.Group, Version: version.Name}).String() && version.Served {
					return
				}
			}
			t.Fatalf("selected reference version %s is not served by %s", selectedVersion, crd.Name)
		})
		switch crd.Spec.Group {
		case "infrastructure.cluster.x-k8s.io":
			if _, found := registry.GetConverter(groupKind); !found {
				continue
			}
		case "cluster.x-k8s.io", "ipam.cluster.x-k8s.io", "addons.cluster.x-k8s.io":
		default:
			continue
		}
		if _, found := registry.GetConverter(groupKind); !found {
			t.Errorf("installed CAPI CRD %s has no converter for %s", crd.Name, groupKind)
		}
		for _, sourceVersion := range crd.Spec.Versions {
			for _, targetVersion := range crd.Spec.Versions {
				if !sourceVersion.Served || !targetVersion.Served || sourceVersion.Name == targetVersion.Name {
					continue
				}
				t.Run(fmt.Sprintf("When %s is converted from %s to %s, it should preserve its identity and annotations", groupKind.Kind, sourceVersion.Name, targetVersion.Name), func(t *testing.T) {
					t.Parallel()
					sourceGVK := schema.GroupVersionKind{Group: groupKind.Group, Version: sourceVersion.Name, Kind: groupKind.Kind}
					targetGVK := schema.GroupVersionKind{Group: groupKind.Group, Version: targetVersion.Name, Kind: groupKind.Kind}
					source, err := scheme.New(sourceGVK)
					if err != nil {
						t.Fatal(err)
					}
					source.GetObjectKind().SetGroupVersionKind(sourceGVK)
					metadata, err := meta.Accessor(source)
					if err != nil {
						t.Fatal(err)
					}
					metadata.SetName("capi-conversion")
					metadata.SetNamespace("clusters")
					metadata.SetAnnotations(map[string]string{"hypershift.openshift.io/test": "preserved"})
					destination, err := scheme.New(targetGVK)
					if err != nil {
						t.Fatal(err)
					}
					convertThroughWebhook(t, handler, source, destination, targetGVK.GroupVersion().String())
					convertedMetadata, err := meta.Accessor(destination)
					if err != nil {
						t.Fatal(err)
					}
					if convertedMetadata.GetName() != metadata.GetName() || convertedMetadata.GetNamespace() != metadata.GetNamespace() {
						t.Fatalf("conversion changed identity: %s/%s", convertedMetadata.GetNamespace(), convertedMetadata.GetName())
					}
					if convertedMetadata.GetAnnotations()["hypershift.openshift.io/test"] != "preserved" {
						t.Fatalf("conversion lost annotations: %v", convertedMetadata.GetAnnotations())
					}
				})
			}
		}
	}

}

func testCAPIRoundTrips(t *testing.T, scheme *runtime.Scheme, handler http.Handler) {
	t.Helper()
	t.Run("When a Cluster round trips through v1beta1, it should preserve references and annotation-backed false values", func(t *testing.T) {
		source := &capiv1beta2.Cluster{
			TypeMeta:   metav1.TypeMeta{APIVersion: capiv1beta2.GroupVersion.String(), Kind: "Cluster"},
			ObjectMeta: metav1.ObjectMeta{Name: "capi-conversion", Namespace: "clusters", Annotations: map[string]string{"hypershift.openshift.io/test": "preserved"}},
			Spec: capiv1beta2.ClusterSpec{
				Paused:            new(false),
				InfrastructureRef: capiv1beta2.ContractVersionedObjectReference{APIGroup: "infrastructure.cluster.x-k8s.io", Kind: "AWSCluster", Name: "infrastructure"},
				ControlPlaneRef:   capiv1beta2.ContractVersionedObjectReference{APIGroup: hyperv1.GroupVersion.Group, Kind: "HostedControlPlane", Name: "control-plane"},
			},
			Status: capiv1beta2.ClusterStatus{Initialization: capiv1beta2.ClusterInitializationStatus{InfrastructureProvisioned: new(false), ControlPlaneInitialized: new(false)}},
		}
		spoke := &capiv1beta1.Cluster{}
		convertThroughWebhook(t, handler, source, spoke, capiv1beta1.GroupVersion.String())
		if spoke.Annotations[conversionutil.DataAnnotation] == "" {
			t.Fatal("down-conversion did not retain hub data in its conversion annotation")
		}
		if spoke.Spec.InfrastructureRef == nil || spoke.Spec.ControlPlaneRef == nil {
			t.Fatal("down-conversion lost Cluster references")
		}
		for _, reference := range []struct {
			name       string
			groupKind  schema.GroupKind
			actualName string
			actualKind string
			version    string
			namespace  string
		}{
			{name: "infrastructure", groupKind: schema.GroupKind{Group: source.Spec.InfrastructureRef.APIGroup, Kind: "AWSCluster"}, actualName: spoke.Spec.InfrastructureRef.Name, actualKind: spoke.Spec.InfrastructureRef.Kind, version: spoke.Spec.InfrastructureRef.APIVersion, namespace: spoke.Spec.InfrastructureRef.Namespace},
			{name: "control-plane", groupKind: schema.GroupKind{Group: hyperv1.GroupVersion.Group, Kind: "HostedControlPlane"}, actualName: spoke.Spec.ControlPlaneRef.Name, actualKind: spoke.Spec.ControlPlaneRef.Kind, version: spoke.Spec.ControlPlaneRef.APIVersion, namespace: spoke.Spec.ControlPlaneRef.Namespace},
		} {
			expectedVersion := scheme.VersionsForGroupKind(reference.groupKind)[0].String()
			if reference.actualName != reference.name || reference.actualKind != reference.groupKind.Kind || reference.version != expectedVersion || reference.namespace != source.Namespace {
				t.Fatalf("conversion corrupted %s reference: %+v", reference.name, reference)
			}
		}
		restored := &capiv1beta2.Cluster{}
		convertThroughWebhook(t, handler, spoke, restored, capiv1beta2.GroupVersion.String())
		if !reflect.DeepEqual(restored.Spec, source.Spec) || !reflect.DeepEqual(restored.Status.Initialization, source.Status.Initialization) {
			t.Fatalf("round trip lost references or explicit false intent: spec=%+v initialization=%+v", restored.Spec, restored.Status.Initialization)
		}
		if restored.Annotations["hypershift.openshift.io/test"] != "preserved" {
			t.Fatal("round trip lost the user annotation")
		}
	})

	deletionTime := metav1.NewTime(time.Date(2026, time.October, 29, 10, 0, 0, 0, time.UTC))
	hubSpec := capiv1beta2.MachineSpec{
		ClusterName:       "hosted-cluster",
		Version:           "v1.35.4",
		InfrastructureRef: capiv1beta2.ContractVersionedObjectReference{APIGroup: "infrastructure.cluster.x-k8s.io", Kind: "AWSMachine", Name: "worker"},
		Bootstrap:         capiv1beta2.Bootstrap{DataSecretName: new("worker-user-data")},
		MinReadySeconds:   new(int32(15)),
		Deletion:          capiv1beta2.MachineDeletionSpec{NodeDrainTimeoutSeconds: new(int32(120)), NodeVolumeDetachTimeoutSeconds: new(int32(240)), NodeDeletionTimeoutSeconds: new(int32(360))},
	}
	spokeSpec := capiv1beta1.MachineSpec{
		ClusterName:             "hosted-cluster",
		Version:                 new("v1.35.4"),
		InfrastructureRef:       corev1.ObjectReference{APIVersion: scheme.VersionsForGroupKind(schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "AWSMachine"})[0].String(), Kind: "AWSMachine", Name: "worker", Namespace: "clusters"},
		Bootstrap:               capiv1beta1.Bootstrap{DataSecretName: new("worker-user-data")},
		NodeDrainTimeout:        &metav1.Duration{Duration: 120 * time.Second},
		NodeVolumeDetachTimeout: &metav1.Duration{Duration: 240 * time.Second},
		NodeDeletionTimeout:     &metav1.Duration{Duration: 360 * time.Second},
	}
	for _, source := range []runtime.Object{
		&capiv1beta2.Machine{TypeMeta: metav1.TypeMeta{APIVersion: capiv1beta2.GroupVersion.String(), Kind: "Machine"}, Spec: hubSpec, Status: capiv1beta2.MachineStatus{Deletion: &capiv1beta2.MachineDeletionStatus{WaitForPreDrainHookStartTime: deletionTime}, Initialization: capiv1beta2.MachineInitializationStatus{InfrastructureProvisioned: new(false), BootstrapDataSecretCreated: new(false)}}},
		&capiv1beta1.Machine{TypeMeta: metav1.TypeMeta{APIVersion: capiv1beta1.GroupVersion.String(), Kind: "Machine"}, Spec: spokeSpec, Status: capiv1beta1.MachineStatus{Deletion: &capiv1beta1.MachineDeletionStatus{WaitForPreDrainHookStartTime: &deletionTime}}},
		&capiv1beta2.MachineDeployment{TypeMeta: metav1.TypeMeta{APIVersion: capiv1beta2.GroupVersion.String(), Kind: "MachineDeployment"}, Spec: capiv1beta2.MachineDeploymentSpec{
			ClusterName: "hosted-cluster", Replicas: new(int32(3)), Paused: new(false),
			Template: capiv1beta2.MachineTemplateSpec{Spec: hubSpec},
			Rollout:  capiv1beta2.MachineDeploymentRolloutSpec{Strategy: capiv1beta2.MachineDeploymentRolloutStrategy{Type: capiv1beta2.RollingUpdateMachineDeploymentStrategyType, RollingUpdate: capiv1beta2.MachineDeploymentRolloutStrategyRollingUpdate{MaxSurge: new(intstr.FromString("25%")), MaxUnavailable: new(intstr.FromInt32(1))}}},
			Deletion: capiv1beta2.MachineDeploymentDeletionSpec{Order: capiv1beta2.OldestMachineSetDeletionOrder},
		}},
		&capiv1beta1.MachineDeployment{TypeMeta: metav1.TypeMeta{APIVersion: capiv1beta1.GroupVersion.String(), Kind: "MachineDeployment"}, Spec: capiv1beta1.MachineDeploymentSpec{
			ClusterName: "hosted-cluster", Replicas: new(int32(3)), MinReadySeconds: new(int32(15)),
			Template: capiv1beta1.MachineTemplateSpec{Spec: spokeSpec},
			Strategy: &capiv1beta1.MachineDeploymentStrategy{Type: capiv1beta1.RollingUpdateMachineDeploymentStrategyType, RollingUpdate: &capiv1beta1.MachineRollingUpdateDeployment{MaxSurge: new(intstr.FromString("25%")), MaxUnavailable: new(intstr.FromInt32(1)), DeletePolicy: new("Oldest")}},
		}},
		&capiv1beta2.MachineSet{TypeMeta: metav1.TypeMeta{APIVersion: capiv1beta2.GroupVersion.String(), Kind: "MachineSet"}, Spec: capiv1beta2.MachineSetSpec{ClusterName: "hosted-cluster", Replicas: new(int32(3)), Template: capiv1beta2.MachineTemplateSpec{Spec: hubSpec}}},
		&capiv1beta1.MachineSet{TypeMeta: metav1.TypeMeta{APIVersion: capiv1beta1.GroupVersion.String(), Kind: "MachineSet"}, Spec: capiv1beta1.MachineSetSpec{ClusterName: "hosted-cluster", Replicas: new(int32(3)), MinReadySeconds: 15, Template: capiv1beta1.MachineTemplateSpec{Spec: spokeSpec}}},
	} {
		sourceGVK := source.GetObjectKind().GroupVersionKind()
		t.Run(fmt.Sprintf("When a nontrivial %s starts in %s, it should preserve its full spec and deletion status after a round trip", sourceGVK.Kind, sourceGVK.Version), func(t *testing.T) {
			metadata, err := meta.Accessor(source)
			if err != nil {
				t.Fatal(err)
			}
			metadata.SetName("worker")
			metadata.SetNamespace("clusters")
			metadata.SetAnnotations(map[string]string{"hypershift.openshift.io/test": "preserved"})
			targetVersion := capiv1beta1.GroupVersion.String()
			if sourceGVK.Version == "v1beta1" {
				targetVersion = capiv1beta2.GroupVersion.String()
			}
			intermediateGVK := schema.FromAPIVersionAndKind(targetVersion, sourceGVK.Kind)
			intermediate, err := scheme.New(intermediateGVK)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := scheme.New(sourceGVK)
			if err != nil {
				t.Fatal(err)
			}
			convertThroughWebhook(t, handler, source, intermediate, targetVersion)
			convertThroughWebhook(t, handler, intermediate, restored, sourceGVK.GroupVersion().String())
			for _, object := range []runtime.Object{intermediate, restored} {
				convertedMeta, err := meta.Accessor(object)
				if err != nil {
					t.Fatal(err)
				}
				if convertedMeta.GetAnnotations()["hypershift.openshift.io/test"] != "preserved" {
					t.Fatal("conversion lost user annotation")
				}
			}
			sourceJSON, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			restoredJSON, err := json.Marshal(restored)
			if err != nil {
				t.Fatal(err)
			}
			var expected, actual map[string]interface{}
			if err := json.Unmarshal(sourceJSON, &expected); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(restoredJSON, &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expected["spec"], actual["spec"]) {
				t.Fatalf("round trip changed the full spec: expected=%s actual=%s", sourceJSON, restoredJSON)
			}
			if sourceGVK.Kind == "Machine" {
				expectedStatus := expected["status"].(map[string]interface{})
				actualStatus := actual["status"].(map[string]interface{})
				for _, field := range []string{"deletion", "initialization"} {
					if !reflect.DeepEqual(expectedStatus[field], actualStatus[field]) {
						t.Fatalf("round trip changed Machine status.%s: expected=%s actual=%s", field, sourceJSON, restoredJSON)
					}
				}
			}
		})
	}

}

func testCAPIFallback(t *testing.T, registry conversion.Registry) {
	t.Helper()
	t.Run("When a HyperShift kind has no registered CAPI converter, it should retain Hub and Convertible fallback in both directions", func(t *testing.T) {
		scheme := runtime.NewScheme()
		spokeVersion := schema.GroupVersion{Group: hyperv1.GroupVersion.Group, Version: "v1testspoke"}
		hubVersion := schema.GroupVersion{Group: hyperv1.GroupVersion.Group, Version: "v1testhub"}
		scheme.AddKnownTypeWithName(spokeVersion.WithKind("HostedCluster"), &conversionFallbackSpoke{})
		scheme.AddKnownTypeWithName(hubVersion.WithKind("HostedCluster"), &conversionFallbackHub{})
		if _, found := registry.GetConverter(hyperv1.GroupVersion.WithKind("HostedCluster").GroupKind()); found {
			t.Fatal("CAPI registry unexpectedly owns HyperShift HostedCluster conversions")
		}
		fallbackHandler := conversion.NewWebhookHandler(scheme, registry)
		source := &conversionFallbackSpoke{HostedCluster: hyperv1.HostedCluster{
			TypeMeta:   metav1.TypeMeta{APIVersion: spokeVersion.String(), Kind: "HostedCluster"},
			ObjectMeta: metav1.ObjectMeta{Name: "fallback", Namespace: "clusters"},
			Spec:       hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64"}},
		}}
		hub := &conversionFallbackHub{}
		convertThroughWebhook(t, fallbackHandler, source, hub, hubVersion.String())
		restored := &conversionFallbackSpoke{}
		convertThroughWebhook(t, fallbackHandler, hub, restored, spokeVersion.String())
		if !reflect.DeepEqual(source.Spec, hub.Spec) || !reflect.DeepEqual(source.Spec, restored.Spec) || source.Name != restored.Name || source.Namespace != restored.Namespace {
			t.Fatal("fallback conversion lost the HostedCluster release or identity")
		}
	})
}

func TestCapiAPIVersionGetter(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name           string
		groupKind      schema.GroupKind
		prioritizeBeta bool
		cancelContext  bool
		expected       string
		expectedError  string
	}{
		{name: "When a core kind is registered, it should select the first registered version", groupKind: capiv1beta1.GroupVersion.WithKind("Machine").GroupKind(), expected: capiv1beta1.GroupVersion.String()},
		{name: "When a provider kind is registered, it should resolve its API version", groupKind: schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "AWSMachine"}, expected: "infrastructure.cluster.x-k8s.io/v1beta2"},
		{name: "When beta2 is prioritized, it should honor scheme priority rather than sort versions", groupKind: capiv1beta1.GroupVersion.WithKind("Machine").GroupKind(), prioritizeBeta: true, expected: capiv1beta2.GroupVersion.String()},
		{name: "When the context is canceled, it should still resolve the registered version", groupKind: capiv1beta1.GroupVersion.WithKind("Machine").GroupKind(), cancelContext: true, expected: capiv1beta1.GroupVersion.String()},
		{name: "When the kind is unknown, it should return the existing error and no version", groupKind: schema.GroupKind{Group: "cluster.x-k8s.io", Kind: "Unknown"}, expectedError: "no versions registered for GroupKind Unknown.cluster.x-k8s.io"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			scheme := freshConversionScheme(t)
			if testCase.prioritizeBeta {
				if err := scheme.SetVersionPriority(capiv1beta2.GroupVersion, capiv1beta1.GroupVersion); err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			if testCase.cancelContext {
				canceledContext, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceledContext
			}
			actual, err := capiAPIVersionGetter(scheme)(ctx, testCase.groupKind)
			if testCase.expectedError != "" {
				if err == nil || err.Error() != testCase.expectedError || actual != "" {
					t.Fatalf("expected empty version and error %q, got version %q and error %v", testCase.expectedError, actual, err)
				}
				return
			}
			if err != nil || actual != testCase.expected {
				t.Fatalf("expected version %q, got %q and error %v", testCase.expected, actual, err)
			}
		})
	}
}

func freshConversionScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for groupVersionKind := range supportapi.Scheme.AllKnownTypes() {
		object, err := supportapi.Scheme.New(groupVersionKind)
		if err != nil {
			t.Fatal(err)
		}
		scheme.AddKnownTypeWithName(groupVersionKind, object)
	}
	for _, version := range supportapi.Scheme.PrioritizedVersionsAllGroups() {
		if err := scheme.SetVersionPriority(supportapi.Scheme.PrioritizedVersionsForGroup(version.Group)...); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func convertThroughWebhook(t *testing.T, handler http.Handler, source, destination runtime.Object, desiredVersion string) {
	t.Helper()
	sourceData, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	review := apiextensionsv1.ConversionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: apiextensionsv1.SchemeGroupVersion.String(), Kind: "ConversionReview"},
		Request:  &apiextensionsv1.ConversionRequest{UID: "capi-conversion-review", DesiredAPIVersion: desiredVersion, Objects: []runtime.RawExtension{{Raw: sourceData}}},
	}
	payload, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/convert", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("conversion returned HTTP %d: %s", recorder.Code, recorder.Body.String())
	}
	var response apiextensionsv1.ConversionReview
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Response == nil || response.Response.Result.Status != metav1.StatusSuccess {
		t.Fatalf("conversion failed: %s", recorder.Body.String())
	}
	if response.Response.UID != review.Request.UID || len(response.Response.ConvertedObjects) != 1 {
		t.Fatalf("unexpected conversion response: %+v", response.Response)
	}
	if err := json.Unmarshal(response.Response.ConvertedObjects[0].Raw, destination); err != nil {
		t.Fatal(err)
	}
	expectedGVK := schema.FromAPIVersionAndKind(desiredVersion, source.GetObjectKind().GroupVersionKind().Kind)
	if destination.GetObjectKind().GroupVersionKind() != expectedGVK {
		t.Fatalf("unexpected destination GVK: %s, want %s", destination.GetObjectKind().GroupVersionKind(), expectedGVK)
	}
}

type conversionFallbackHub struct {
	hyperv1.HostedCluster
}

func (hub *conversionFallbackHub) Hub() {}

func (hub *conversionFallbackHub) DeepCopyObject() runtime.Object {
	return &conversionFallbackHub{HostedCluster: *hub.HostedCluster.DeepCopy()}
}

type conversionFallbackSpoke struct {
	hyperv1.HostedCluster
}

func (spoke *conversionFallbackSpoke) DeepCopyObject() runtime.Object {
	return &conversionFallbackSpoke{HostedCluster: *spoke.HostedCluster.DeepCopy()}
}

func (spoke *conversionFallbackSpoke) ConvertTo(destination crconversion.Hub) error {
	hub := destination.(*conversionFallbackHub)
	typeMeta := hub.TypeMeta
	hub.HostedCluster = *spoke.HostedCluster.DeepCopy()
	hub.TypeMeta = typeMeta
	return nil
}

func (spoke *conversionFallbackSpoke) ConvertFrom(source crconversion.Hub) error {
	typeMeta := spoke.TypeMeta
	spoke.HostedCluster = *source.(*conversionFallbackHub).HostedCluster.DeepCopy()
	spoke.TypeMeta = typeMeta
	return nil
}
