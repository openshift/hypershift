package pullsecret

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest/dockercredentials"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

	// The credential store treats these keys as the same registry. If both survived
	// the merge it would hold two credentials for one key and pick one in map
	// iteration order, so the provider credential would only win some of the time.
	t.Run("When base has keys the credential store treats as the additional host, it should keep only the additional entry", func(t *testing.T) {
		g := NewWithT(t)
		basicAuth := func(user string) string {
			return base64.StdEncoding.EncodeToString([]byte(user + ":secret"))
		}
		equivalentBase := []byte(`{"auths":{` +
			`"https://registry.example.test/v1/":{"auth":"` + basicAuth("base-v1") + `"},` +
			`"http://registry.example.test":{"auth":"` + basicAuth("base-http") + `"},` +
			`"registry.example.test/team/repo":{"auth":"` + basicAuth("base-repo") + `"},` +
			`"other.example.test":{"auth":"` + basicAuth("other") + `"}}}`)
		provider := []byte(`{"auths":{"registry.example.test":{"auth":"` + basicAuth("provider") + `"}}}`)

		merged, err := Merge(equivalentBase, provider)
		g.Expect(err).ToNot(HaveOccurred())

		auths := authsIn(t, g, merged)
		g.Expect(auths).To(HaveLen(3))
		g.Expect(auths).To(HaveKey("registry.example.test"))
		g.Expect(auths).ToNot(HaveKey("https://registry.example.test/v1/"))
		g.Expect(auths).ToNot(HaveKey("http://registry.example.test"))
		// A repository scoped key is a different key to the credential store, so it
		// is kept, as is the unrelated host.
		g.Expect(auths).To(HaveKey("registry.example.test/team/repo"))
		g.Expect(auths).To(HaveKey("other.example.test"))

		// The selection used to depend on map iteration order, so check it repeatedly.
		for range 50 {
			store, err := dockercredentials.NewFromBytes(merged)
			g.Expect(err).ToNot(HaveOccurred())
			user, _ := store.Basic(&url.URL{Scheme: "https", Host: "registry.example.test", Path: "/v2/"})
			g.Expect(user).To(Equal("provider"))
		}
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

		got, err := ForControlPlaneRelease(context.Background(), c, SourceSecretKey(namespace, ""), base)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(got).To(Equal(base))
	})

	t.Run("merges the secret when present", func(t *testing.T) {
		g := NewWithT(t)
		secret := newSecret(map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"cve.example.com":{"auth":"cve-token"}}}`),
		})
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

		got, err := ForControlPlaneRelease(context.Background(), c, SourceSecretKey(namespace, ""), base)
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

		got, err := ForControlPlaneRelease(context.Background(), c, SourceSecretKey(namespace, ""), base)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(got).To(Equal(base))
	})

	t.Run("When a secret name is configured, it should merge that secret and ignore the default one", func(t *testing.T) {
		g := NewWithT(t)
		defaultNamed := newSecret(map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"default.example.com":{"auth":"default-token"}}}`),
		})
		configured := newSecret(map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"cve.example.com":{"auth":"cve-token"}}}`),
		})
		configured.Name = "generated-by-another-system"
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(defaultNamed, configured).Build()

		got, err := ForControlPlaneRelease(context.Background(), c, SourceSecretKey(namespace, "generated-by-another-system"), base)
		g.Expect(err).ToNot(HaveOccurred())

		auths := authsIn(t, g, got)
		g.Expect(auths).To(HaveKey("registry.redhat.io"))
		g.Expect(auths).To(HaveKey("cve.example.com"))
		g.Expect(auths).ToNot(HaveKey("default.example.com"))
	})

	t.Run("errors when the secret lacks the dockerconfigjson key", func(t *testing.T) {
		g := NewWithT(t)
		c := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(newSecret(map[string][]byte{"wrong-key": []byte("{}")})).Build()

		_, err := ForControlPlaneRelease(context.Background(), c, SourceSecretKey(namespace, ""), base)
		g.Expect(err).To(HaveOccurred())
	})
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		payload   []byte
		expectErr bool
	}{
		{name: "When the payload has an auths object, it should accept it", payload: []byte(`{"auths":{"quay.io":{"auth":"dXNlcjpwYXNz"}}}`)},
		{name: "When the auths object is empty, it should accept it", payload: []byte(`{"auths":{}}`)},
		// Kubernetes accepts this payload in a kubernetes.io/dockerconfigjson Secret.
		{name: "When auths is a list, it should return an error", payload: []byte(`{"auths":[]}`), expectErr: true},
		{name: "When the auths key is missing, it should return an error", payload: []byte(`{}`), expectErr: true},
		{name: "When the payload is not JSON, it should return an error", payload: []byte(`not json`), expectErr: true},
		{name: "When one entry's auth is not a string, it should return an error", payload: []byte(`{"auths":{"quay.io":{"auth":"dXNlcjpwYXNz"},"registry.example.com":{"auth":42}}}`), expectErr: true},
		{name: "When one entry's auth is not base64, it should return an error", payload: []byte(`{"auths":{"registry.example.com":{"auth":"not base64!"}}}`), expectErr: true},
		{name: "When one entry's auth does not decode to username:password, it should return an error", payload: []byte(`{"auths":{"registry.example.com":{"auth":"bm9jb2xvbg=="}}}`), expectErr: true},
		{name: "When an entry carries a username and password instead of auth, it should accept it", payload: []byte(`{"auths":{"registry.example.com":{"username":"user","password":"pass"}}}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			err := Validate(tc.payload)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			// Whatever passes must be accepted by Merge, which every reader uses, and
			// its result by the registry clients' credential store.
			merged, mergeErr := Merge([]byte(`{"auths":{}}`), tc.payload)
			g.Expect(mergeErr).ToNot(HaveOccurred())
			_, storeErr := dockercredentials.NewFromBytes(merged)
			g.Expect(storeErr).ToNot(HaveOccurred())
		})
	}
}

func TestSourceSecretKey(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		expected   types.NamespacedName
	}{
		{
			name:     "When no name is configured, it should return the default name in the operator namespace",
			expected: types.NamespacedName{Namespace: "hypershift", Name: ControlPlaneReleaseSecretName},
		},
		{
			name:       "When a name is configured, it should return that name in the operator namespace",
			configured: "generated-by-another-system",
			expected:   types.NamespacedName{Namespace: "hypershift", Name: "generated-by-another-system"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(SourceSecretKey("hypershift", tt.configured)).To(Equal(tt.expected))
		})
	}
}

func TestCredentialStoreKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{name: "When the key is a bare host, it should be returned unchanged", key: "registry.example.test", expected: "registry.example.test"},
		{name: "When the key has a trailing slash, it should drop it", key: "registry.example.test/", expected: "registry.example.test"},
		{name: "When the key has an https scheme, it should drop the scheme", key: "https://registry.example.test", expected: "registry.example.test"},
		{name: "When the key has an http scheme, it should drop the scheme", key: "http://registry.example.test", expected: "registry.example.test"},
		{name: "When the key has a /v1/ path, it should drop the path", key: "https://registry.example.test/v1/", expected: "registry.example.test"},
		{name: "When the key has a /v2/ path and no scheme, it should drop the path", key: "registry.example.test/v2/", expected: "registry.example.test"},
		{name: "When the key has a port, it should keep the port", key: "registry.example.test:5000", expected: "registry.example.test:5000"},
		{name: "When the key is repository scoped, it should keep the repository", key: "registry.example.test/team/repo", expected: "registry.example.test/team/repo"},
		{name: "When the key has a /v2/ prefix before a repository, it should keep the repository", key: "https://registry.example.test/v2/team", expected: "registry.example.test/team"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(credentialStoreKey(tc.key)).To(Equal(tc.expected))
		})
	}
}

func TestForHostedControlPlane(t *testing.T) {
	const namespace = "clusters-example"
	base := []byte(`{"auths":{"registry.redhat.io":{"auth":"base-token"}}}`)
	providerAuths := []byte(`{"auths":{"cve.example.com":{"auth":"cve-token"}}}`)

	scheme := runtime.NewScheme()
	g := NewWithT(t)
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())

	pinned := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: namespace},
		Spec:       hyperv1.HostedControlPlaneSpec{ControlPlaneReleaseImage: ptrTo("cve.example.com/ocp/release:4.19.1")},
	}
	syncedCopy := func(labels map[string]string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: SyncedControlPlaneReleaseSecretName, Namespace: namespace, Labels: labels},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: providerAuths},
		}
	}

	tests := []struct {
		name         string
		hcp          *hyperv1.HostedControlPlane
		objects      []*corev1.Secret
		expectErr    bool
		expectMerged bool
	}{
		{
			name:         "When the control plane pins a release and the managed copy exists, it should merge the copy",
			hcp:          pinned,
			objects:      []*corev1.Secret{syncedCopy(map[string]string{ManagedByLabel: "true"})},
			expectMerged: true,
		},
		{
			name: "When the control plane pins no release, it should return base unchanged",
			hcp:  &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: namespace}},
			objects: []*corev1.Secret{
				syncedCopy(map[string]string{ManagedByLabel: "true"}),
			},
		},
		{
			name: "When the copy is absent, it should return base unchanged",
			hcp:  pinned,
		},
		{
			name: "When only the source name exists in the namespace, it should not read it",
			hcp:  pinned,
			objects: []*corev1.Secret{{
				ObjectMeta: metav1.ObjectMeta{Name: ControlPlaneReleaseSecretName, Namespace: namespace},
				Data:       map[string][]byte{corev1.DockerConfigJsonKey: providerAuths},
			}},
		},
		{
			name:      "When a secret with the synced name lacks the managed label, it should return an error",
			hcp:       pinned,
			objects:   []*corev1.Secret{syncedCopy(nil)},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			builder := fake.NewClientBuilder().WithScheme(scheme)
			for _, obj := range tc.objects {
				builder = builder.WithObjects(obj)
			}

			got, err := ForHostedControlPlane(t.Context(), builder.Build(), tc.hcp, base)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			if !tc.expectMerged {
				g.Expect(got).To(Equal(base))
				return
			}
			auths := authsIn(t, g, got)
			g.Expect(auths).To(HaveKey("registry.redhat.io"))
			g.Expect(auths).To(HaveKey("cve.example.com"))
		})
	}
}

// AutoNode passes a synthetic HostedCluster whose namespace already is the control
// plane namespace, so the namespace must be taken from the caller, not derived.
func TestForHostedClusterInNamespace(t *testing.T) {
	g := NewWithT(t)
	const controlPlaneNamespace = "clusters-example"
	base := []byte(`{"auths":{"registry.redhat.io":{"auth":"base-token"}}}`)

	scheme := runtime.NewScheme()
	g.Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SyncedControlPlaneReleaseSecretName,
			Namespace: controlPlaneNamespace,
			Labels:    map[string]string{ManagedByLabel: "true"},
		},
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{"cve.example.com":{"auth":"cve-token"}}}`)},
	}).Build()

	synthetic := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: controlPlaneNamespace},
		Spec:       hyperv1.HostedClusterSpec{ControlPlaneRelease: &hyperv1.Release{Image: "cve.example.com/ocp/release:4.19.1"}},
	}
	got, err := ForHostedClusterInNamespace(t.Context(), c, synthetic, controlPlaneNamespace, base)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(authsIn(t, g, got)).To(HaveKey("cve.example.com"))
}

func ptrTo[T any](v T) *T { return &v }
