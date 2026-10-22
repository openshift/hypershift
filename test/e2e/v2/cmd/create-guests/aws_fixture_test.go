//go:build e2ev2

package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/blang/semver"
)

func TestConfigureAWSFixture(t *testing.T) {
	t.Run("When initial configuration is absent, it should initialize baseline NLB and only the requested custom inputs", func(t *testing.T) {
		cluster := &hyperv1.HostedCluster{}
		configureAWSFixture(cluster, "public", semver.MustParse("4.21.0"))
		assertAWSFixtureNLB(t, cluster, nil)
		if cluster.Spec.Configuration.Image != nil {
			t.Fatal("public fixture must configure baseline NLB without unrelated image configuration")
		}
		configureAWSFixture(cluster, "custom-config", semver.MustParse("4.21.0"))
		if cluster.Spec.Configuration == nil || cluster.Spec.Configuration.Image == nil || len(cluster.Spec.Configuration.Image.RegistrySources.BlockedRegistries) != 1 {
			t.Fatal("custom fixture should initialize initial blocked registry configuration")
		}
	})
	for _, version := range []string{"4.14.0", "4.16.0", "4.17.0", "4.18.0", "4.19.0", "4.20.0", "4.21.0", "4.21.0-0.nightly-2026-10-20-000000", "5.0.0"} {
		for _, variant := range []string{"public", "custom-config", "private", "private-route", "managed-dns", "upgrade", "karpenter", "karpenter-upgrade"} {
			t.Run(fmt.Sprintf("When %s uses %s, it should preserve version-specific initial configuration", variant, version), func(t *testing.T) {
				parsed := semver.MustParse(version)
				parsed.Patch, parsed.Pre, parsed.Build = 0, nil, nil
				cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"hypershift.openshift.io/cleanup-cloud-resources": "true"}}}
				cluster.Spec.Labels = map[string]string{"hypershift-e2e-test-label": "test", "unrelated": "retained"}
				cluster.Spec.Configuration = &hyperv1.ClusterConfiguration{
					FeatureGate: &configv1.FeatureGateSpec{FeatureGateSelection: configv1.FeatureGateSelection{FeatureSet: configv1.TechPreviewNoUpgrade}},
					Network:     &configv1.NetworkSpec{ExternalIP: &configv1.ExternalIPConfig{}},
					Image:       &configv1.ImageSpec{RegistrySources: configv1.RegistrySources{InsecureRegistries: []string{"registry.example.test:5000"}}},
					Ingress:     &configv1.IngressSpec{Domain: "apps.example.test", AppsDomain: "custom.example.test"},
				}
				original := cluster.DeepCopy()
				configureAWSFixture(cluster, variant, semver.MustParse(version))
				if (cluster.Spec.Labels["hypershift-e2e-test-label"] != "") != parsed.GTE(semver.MustParse("4.19.0")) || cluster.Spec.Labels["unrelated"] != "retained" {
					t.Fatal("pod label applicability or unrelated label changed")
				}
				if (cluster.Spec.Configuration.FeatureGate != nil) != parsed.GTE(semver.MustParse("4.18.0")) {
					t.Fatal("TechPreview fixture release gate changed")
				}
				wantCleanup := variant != "private" && variant != "private-route" || parsed.GTE(semver.MustParse("4.15.0"))
				if (cluster.Annotations["hypershift.openshift.io/cleanup-cloud-resources"] != "") != wantCleanup {
					t.Fatal("private cleanup annotation release gate changed")
				}
				wantIngress := variant == "public" && parsed.GTE(semver.MustParse("4.21.0"))
				if (cluster.Spec.OperatorConfiguration != nil) != wantIngress {
					t.Fatal("initial internal ingress release gate changed")
				}
				if wantIngress && cluster.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy.LoadBalancer.Scope != operatorv1.InternalLoadBalancer {
					t.Fatal("public fixture must initially request internal ingress")
				}
				if !reflect.DeepEqual(cluster.Spec.Configuration.Network, original.Spec.Configuration.Network) || !reflect.DeepEqual(cluster.Spec.Configuration.Image.RegistrySources.InsecureRegistries, original.Spec.Configuration.Image.RegistrySources.InsecureRegistries) {
					t.Fatal("fixture mutation changed unrelated configuration")
				}
				assertAWSFixtureNLB(t, cluster, original.Spec.Configuration.Ingress)
				if variant == "custom-config" {
					if !reflect.DeepEqual(cluster.Spec.Configuration.Image.RegistrySources.BlockedRegistries, []string{"badregistry.io"}) {
						t.Fatal("blocked registry must exist before first apply")
					}
					wantCapabilities := 0
					if parsed.GTE(semver.MustParse("4.18.0")) {
						wantCapabilities = 4
					}
					if parsed.GTE(semver.MustParse("4.20.0")) {
						wantCapabilities = 6
					}
					if wantCapabilities == 0 && cluster.Spec.Capabilities != nil || wantCapabilities > 0 && (cluster.Spec.Capabilities == nil || len(cluster.Spec.Capabilities.Disabled) != wantCapabilities) {
						t.Fatal("disabled capabilities do not match the release branch")
					}
				}
			})
		}
	}
}

func assertAWSFixtureNLB(t *testing.T, cluster *hyperv1.HostedCluster, original *configv1.IngressSpec) {
	t.Helper()
	if cluster.Spec.Configuration == nil || cluster.Spec.Configuration.Ingress == nil || cluster.Spec.Configuration.Ingress.LoadBalancer.Platform.Type != configv1.AWSPlatformType || cluster.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS == nil || cluster.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS.Type != configv1.NLB {
		t.Fatal("every AWS fixture and release must configure baseline AWS NLB ingress")
	}
	if original != nil && (cluster.Spec.Configuration.Ingress.Domain != original.Domain || cluster.Spec.Configuration.Ingress.AppsDomain != original.AppsDomain) {
		t.Fatal("baseline NLB configuration must preserve existing ingress domains")
	}
}

func TestReadAWSFixture(t *testing.T) {
	manifest := "apiVersion: hypershift.openshift.io/v1beta1\nkind: HostedCluster\nmetadata:\n  name: fixture\nspec: {}\n---\napiVersion: hypershift.openshift.io/v1beta1\nkind: NodePool\nmetadata:\n  name: pool\nspec:\n  nodeVolumeDetachTimeout: 5m\n"
	for _, version := range []string{"4.16.0", "4.17.0", "4.17.0-0.nightly-2026-10-20-000000"} {
		t.Run("When rendering "+version+", it should omit only unsupported NodePool fields", func(t *testing.T) {
			objects, err := readAWSFixture(strings.NewReader(manifest), namedSpec{name: "fixture"}, semver.MustParse(version))
			if err != nil || len(objects) != 2 {
				t.Fatalf("reading fixture: %v", err)
			}
			_, present, err := unstructured.NestedString(objects[1].Object, "spec", "nodeVolumeDetachTimeout")
			if err != nil || present != (semver.MustParse(version).Minor >= 17) {
				t.Fatalf("unexpected detach timeout applicability: %v, %v", present, err)
			}
		})
	}
	for _, invalid := range []string{"", "{invalid", strings.ReplaceAll(manifest, "fixture", "different"), manifest + "---\n" + manifest} {
		if _, err := readAWSFixture(strings.NewReader(invalid), namedSpec{name: "fixture"}, semver.MustParse("4.21.0")); err == nil {
			t.Fatal("expected malformed, missing, mismatched or duplicate HostedCluster fixture to fail before any apply")
		}
	}
	t.Run("When decoded intent reaches the first API Create, it should already contain baseline AWS NLB configuration", testReadAWSFixtureNLB)
}

func TestApplyAWSFixture(t *testing.T) {
	for _, failApply := range []bool{false, true} {
		t.Run(fmt.Sprintf("When applying with failure=%t, it should preserve CLI ownership and stop on errors", failApply), func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{TypeMeta: metav1.TypeMeta{APIVersion: "hypershift.openshift.io/v1beta1", Kind: "HostedCluster"}, ObjectMeta: metav1.ObjectMeta{Name: "fixture", Namespace: "clusters"}}
			configureAWSFixture(cluster, "custom-config", semver.MustParse("4.21.0"))
			converted, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cluster)
			if err != nil {
				t.Fatal(err)
			}
			objects := []*unstructured.Unstructured{
				{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]interface{}{"name": "pull-secret", "namespace": "clusters"}}},
				{Object: converted},
			}
			var calls []string
			sentinel := errors.New("injected apply failure")
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, patch crclient.Patch, options ...crclient.PatchOption) error {
					calls = append(calls, "patch")
					settings := &crclient.PatchOptions{}
					for _, option := range options {
						option.ApplyToPatch(settings)
					}
					if patch.Type() != crclient.Apply.Type() || settings.Force == nil || !*settings.Force || settings.FieldManager != "hypershift-cli" || object.GetLabels()[cmdutil.AutoInfraLabelName] != "fixture" {
						t.Fatal("rendered resource lost CLI SSA or infrastructure-label semantics")
					}
					if failApply {
						return sentinel
					}
					return nil
				},
				Create: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, options ...crclient.CreateOption) error {
					calls = append(calls, "create")
					blocked, _, err := unstructured.NestedStringSlice(object.(*unstructured.Unstructured).Object, "spec", "configuration", "image", "registrySources", "blockedRegistries")
					if err != nil || !reflect.DeepEqual(blocked, []string{"badregistry.io"}) || object.GetLabels()[cmdutil.AutoInfraLabelName] != "fixture" {
						t.Fatal("HostedCluster Create did not receive the initial fixture configuration")
					}
					loadBalancer, _, err := unstructured.NestedString(object.(*unstructured.Unstructured).Object, "spec", "configuration", "ingress", "loadBalancer", "platform", "aws", "type")
					if err != nil || loadBalancer != string(configv1.NLB) {
						t.Fatal("first HostedCluster Create must receive the baseline NLB fixture")
					}
					return nil
				},
			}).Build()
			err = applyAWSFixture(t.Context(), client, "fixture", objects)
			if failApply {
				if !errors.Is(err, sentinel) || !reflect.DeepEqual(calls, []string{"patch"}) {
					t.Fatalf("apply should stop before HostedCluster Create: %v, %v", calls, err)
				}
			} else if err != nil || !reflect.DeepEqual(calls, []string{"patch", "create"}) {
				t.Fatalf("apply order changed: %v, %v", calls, err)
			}
		})
	}
}

func testReadAWSFixtureNLB(t *testing.T) {
	t.Helper()
	for _, variant := range []string{"public", "private", "private-route", "managed-dns", "upgrade", "karpenter", "karpenter-upgrade"} {
		t.Run("When "+variant+" is rendered and applied, it should configure NLB before the first HostedCluster Create", func(t *testing.T) {
			rendered := `apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: fixture
  namespace: clusters
spec:
  configuration:
    ingress:
      domain: apps.example.test
    network:
      networkType: OVNKubernetes
`
			objects, err := readAWSFixture(strings.NewReader(rendered), namedSpec{ClusterSpec: lifecycle.ClusterSpec{Variant: variant}, name: "fixture"}, semver.MustParse("4.21.0"))
			if err != nil {
				t.Fatal(err)
			}
			creates := 0
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, options ...crclient.CreateOption) error {
					creates++
					cluster := &hyperv1.HostedCluster{}
					if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.(*unstructured.Unstructured).Object, cluster); err != nil {
						return err
					}
					if cluster.Spec.Configuration == nil || cluster.Spec.Configuration.Ingress == nil || cluster.Spec.Configuration.Ingress.LoadBalancer.Platform.Type != configv1.AWSPlatformType || cluster.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS == nil || cluster.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS.Type != configv1.NLB || cluster.Spec.Configuration.Ingress.Domain != "apps.example.test" || cluster.Spec.Configuration.Network == nil || cluster.Spec.Configuration.Network.NetworkType != "OVNKubernetes" {
						t.Fatal("first API Create lost NLB intent or unrelated rendered configuration")
					}
					return nil
				},
			}).Build()
			if err := applyAWSFixture(t.Context(), client, "fixture", objects); err != nil {
				t.Fatal(err)
			}
			if creates != 1 {
				t.Fatalf("expected exactly one initial Create, got %d", creates)
			}
		})
	}
}
