package ignitionpayload

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func mc(name string) string {
	return fmt.Sprintf(`apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: %s
spec:
  config:
    ignition:
      version: 3.2.0
`, name)
}

func cfgMap(ns, name, cfg string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Data:       map[string]string{configDataKey: cfg},
	}
}

func TestResolveAndValidate(t *testing.T) {
	t.Run("valid config splits rollout vs management and is deterministic", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()

		objs := []client.Object{
			cfgMap("hcp", "user-a", mc("00-user-a")),
			cfgMap("hcp", "core-fips", mc("00-core-fips")),
			cfgMap("hcp", "haproxy", mc("00-haproxy")),
		}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()
		cr := &hyperv1.IgnitionPayload{
			ObjectMeta: metav1.ObjectMeta{Namespace: "hcp", Name: "np-1"},
			Spec: hyperv1.IgnitionPayloadSpec{
				RolloutConfigMaps:    []hyperv1.ConfigMapReference{{Name: "user-a"}, {Name: "core-fips"}},
				ManagementConfigMaps: []hyperv1.ConfigMapReference{{Name: "haproxy"}},
			},
		}

		got, err := resolveAndValidate(ctx, c, cr)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(got.RolloutManifests).To(ContainSubstring("00-core-fips"))
		g.Expect(got.RolloutManifests).To(ContainSubstring("00-user-a"))
		g.Expect(got.RolloutManifests).ToNot(ContainSubstring("00-haproxy"))
		g.Expect(got.ManagementManifests).To(ContainSubstring("00-haproxy"))
		// MachineConfig defaulting applied the worker role.
		g.Expect(got.RolloutManifests).To(ContainSubstring("machineconfiguration.openshift.io/role: worker"))

		// Determinism: order of spec refs does not change the output.
		cr2 := cr.DeepCopy()
		cr2.Spec.RolloutConfigMaps = []hyperv1.ConfigMapReference{{Name: "core-fips"}, {Name: "user-a"}}
		got2, err := resolveAndValidate(ctx, c, cr2)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(got2.RolloutManifests).To(Equal(got.RolloutManifests))
	})

	t.Run("an invalid manifest fails validation and names the source ConfigMap", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()

		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
			cfgMap("hcp", "bad", "apiVersion: v1\nkind: NotAThing\n"),
		).Build()
		cr := &hyperv1.IgnitionPayload{
			ObjectMeta: metav1.ObjectMeta{Namespace: "hcp", Name: "np-1"},
			Spec: hyperv1.IgnitionPayloadSpec{
				RolloutConfigMaps: []hyperv1.ConfigMapReference{{Name: "bad"}},
			},
		}

		_, err := resolveAndValidate(ctx, c, cr)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("bad")) // names the source ConfigMap
	})
}
