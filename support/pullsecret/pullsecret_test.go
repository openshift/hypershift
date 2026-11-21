package pullsecret

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func authsIn(t *testing.T, g Gomega, raw []byte) map[string]any {
	t.Helper()
	var parsed map[string]any
	g.Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
	auths, ok := parsed["auths"].(map[string]any)
	g.Expect(ok).To(BeTrue(), "expected an auths object")
	return auths
}

func TestMerge(t *testing.T) {
	base := []byte(`{"auths":{"registry.redhat.io":{"auth":"base-token"}}}`)
	additional := []byte(`{"auths":{"cve.example.com":{"auth":"cve-token"}}}`)

	t.Run("combines auths from both secrets", func(t *testing.T) {
		g := NewWithT(t)

		merged, err := Merge(base, additional)
		g.Expect(err).ToNot(HaveOccurred())

		auths := authsIn(t, g, merged)
		g.Expect(auths).To(HaveLen(2))
		g.Expect(auths).To(HaveKey("registry.redhat.io"))
		g.Expect(auths).To(HaveKey("cve.example.com"))
	})

	// The additional credential must be able to replace a host entry already present
	// in the cluster pull secret. The registry client resolves credentials per host
	// and ignores repository scoped keys, so overriding the host entry is the only
	// way to reach a private repository on a shared registry such as quay.io.
	t.Run("additional wins on conflicting registry", func(t *testing.T) {
		g := NewWithT(t)
		conflicting := []byte(`{"auths":{"registry.redhat.io":{"auth":"override-token"}}}`)

		merged, err := Merge(base, conflicting)
		g.Expect(err).ToNot(HaveOccurred())

		auths := authsIn(t, g, merged)
		g.Expect(auths).To(HaveLen(1))
		entry, ok := auths["registry.redhat.io"].(map[string]any)
		g.Expect(ok).To(BeTrue())
		g.Expect(entry["auth"]).To(Equal("override-token"))
	})

	t.Run("overriding one host leaves other base entries intact", func(t *testing.T) {
		g := NewWithT(t)
		multiBase := []byte(`{"auths":{"registry.redhat.io":{"auth":"base-token"},"cloud.openshift.com":{"auth":"keep-me"}}}`)
		conflicting := []byte(`{"auths":{"registry.redhat.io":{"auth":"override-token"}}}`)

		merged, err := Merge(multiBase, conflicting)
		g.Expect(err).ToNot(HaveOccurred())

		auths := authsIn(t, g, merged)
		g.Expect(auths).To(HaveLen(2))
		g.Expect(auths["registry.redhat.io"].(map[string]any)["auth"]).To(Equal("override-token"))
		g.Expect(auths["cloud.openshift.com"].(map[string]any)["auth"]).To(Equal("keep-me"))
	})

	t.Run("returns base unchanged when additional is empty", func(t *testing.T) {
		g := NewWithT(t)

		merged, err := Merge(base, nil)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(merged).To(Equal(base))
	})

	t.Run("preserves opaque fields of an auth entry", func(t *testing.T) {
		g := NewWithT(t)
		rich := []byte(`{"auths":{"cve.example.com":{"auth":"cve-token","email":"a@b.c","username":"u"}}}`)

		merged, err := Merge(base, rich)
		g.Expect(err).ToNot(HaveOccurred())

		entry, ok := authsIn(t, g, merged)["cve.example.com"].(map[string]any)
		g.Expect(ok).To(BeTrue())
		g.Expect(entry["auth"]).To(Equal("cve-token"))
		g.Expect(entry["email"]).To(Equal("a@b.c"))
		g.Expect(entry["username"]).To(Equal("u"))
	})

	t.Run("errors on malformed input", func(t *testing.T) {
		for name, tc := range map[string]struct{ base, additional []byte }{
			"base not json":         {[]byte(`not json`), additional},
			"additional not json":   {base, []byte(`not json`)},
			"base missing auths":    {[]byte(`{}`), additional},
			"additional no auths":   {base, []byte(`{}`)},
			"auths is not object":   {base, []byte(`{"auths":"nope"}`)},
			"base auths not object": {[]byte(`{"auths":5}`), additional},
		} {
			t.Run(name, func(t *testing.T) {
				g := NewWithT(t)
				_, err := Merge(tc.base, tc.additional)
				g.Expect(err).To(HaveOccurred())
			})
		}
	})
}

func TestForControlPlaneRelease(t *testing.T) {
	const namespace = "clusters-example"
	base := []byte(`{"auths":{"registry.redhat.io":{"auth":"base-token"}}}`)

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}

	newSecret := func(data map[string][]byte) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: ControlPlaneReleaseSecretName, Namespace: namespace},
			Data:       data,
		}
	}

	t.Run("returns base unchanged when the secret is absent", func(t *testing.T) {
		g := NewWithT(t)
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		got, err := ForControlPlaneRelease(context.Background(), c, namespace, base)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(got).To(Equal(base))
	})

	t.Run("merges the secret when present", func(t *testing.T) {
		g := NewWithT(t)
		secret := newSecret(map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"cve.example.com":{"auth":"cve-token"}}}`),
		})
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

		got, err := ForControlPlaneRelease(context.Background(), c, namespace, base)
		g.Expect(err).ToNot(HaveOccurred())

		auths := authsIn(t, g, got)
		g.Expect(auths).To(HaveKey("registry.redhat.io"))
		g.Expect(auths).To(HaveKey("cve.example.com"))
	})

	t.Run("ignores a secret in a different namespace", func(t *testing.T) {
		g := NewWithT(t)
		other := newSecret(map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"cve.example.com":{"auth":"cve-token"}}}`),
		})
		other.Namespace = "some-other-namespace"
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(other).Build()

		got, err := ForControlPlaneRelease(context.Background(), c, namespace, base)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(got).To(Equal(base))
	})

	t.Run("errors when the secret lacks the dockerconfigjson key", func(t *testing.T) {
		g := NewWithT(t)
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(newSecret(map[string][]byte{"wrong-key": []byte("{}")})).Build()

		_, err := ForControlPlaneRelease(context.Background(), c, namespace, base)
		g.Expect(err).To(HaveOccurred())
	})
}
