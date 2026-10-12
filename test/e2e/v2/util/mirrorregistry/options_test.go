package mirrorregistry

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestWithDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   Options
		want Options
	}{
		{
			name: "When options are zero-valued, it should apply every default",
			in:   Options{},
			want: Options{Namespace: DefaultNamespace, Name: DefaultName, Image: DefaultRegistryImage, Storage: StorageOptions{Size: resource.MustParse(DefaultStorageSize)}},
		},
		{
			name: "When options are configured, it should preserve their values",
			in: Options{
				Namespace: "registry-tests", Name: "mirror-test", Image: "quay.io/libpod/registry:2.8.2",
				IngressDomain: "apps.alesross-mc.alesross.hypershift.devcluster.openshift.com",
				ExtraDNSNames: []string{"*.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com"},
				CA:            &KeyPair{CertPEM: []byte("certificate"), KeyPEM: []byte("private key")},
				Storage:       StorageOptions{Size: resource.MustParse("8Gi"), StorageClassName: "fast"}, CopierImage: "quay.io/openshift/cli:latest", Copier: testCopier{},
			},
			want: Options{
				Namespace: "registry-tests", Name: "mirror-test", Image: "quay.io/libpod/registry:2.8.2",
				IngressDomain: "apps.alesross-mc.alesross.hypershift.devcluster.openshift.com",
				ExtraDNSNames: []string{"*.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com"},
				CA:            &KeyPair{CertPEM: []byte("certificate"), KeyPEM: []byte("private key")},
				Storage:       StorageOptions{Size: resource.MustParse("8Gi"), StorageClassName: "fast"}, CopierImage: "quay.io/openshift/cli:latest", Copier: testCopier{},
			},
		},
		{
			name: "When only storage size is zero, it should default storage size and preserve storage class",
			in:   Options{Storage: StorageOptions{StorageClassName: "fast"}},
			want: Options{Namespace: DefaultNamespace, Name: DefaultName, Image: DefaultRegistryImage, Storage: StorageOptions{Size: resource.MustParse(DefaultStorageSize), StorageClassName: "fast"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.withDefaults()
			if got.Namespace != tt.want.Namespace || got.Name != tt.want.Name || got.Image != tt.want.Image || got.IngressDomain != tt.want.IngressDomain || !got.Storage.Size.Equal(tt.want.Storage.Size) || got.Storage.StorageClassName != tt.want.Storage.StorageClassName || got.CopierImage != tt.want.CopierImage || got.CA != tt.in.CA || got.Copier != tt.in.Copier || len(got.ExtraDNSNames) != len(tt.want.ExtraDNSNames) {
				t.Fatalf("withDefaults() = %#v, want %#v", got, tt.want)
			}
			for i := range tt.want.ExtraDNSNames {
				if got.ExtraDNSNames[i] != tt.want.ExtraDNSNames[i] {
					t.Fatalf("withDefaults() ExtraDNSNames = %#v, want %#v", got.ExtraDNSNames, tt.want.ExtraDNSNames)
				}
			}
		})
	}
}

func TestValidate(t *testing.T) {
	valid := Options{Name: "mirror-registry", Namespace: "test-mirror-registry", CopierImage: "quay.io/openshift/cli:latest"}
	tests := []struct {
		name    string
		options Options
		wantErr bool
		want    []string
	}{
		{name: "When names are valid and copier image is configured, it should accept the options", options: valid},
		{name: "When a custom copier is provided without copier image, it should accept the options", options: Options{Name: valid.Name, Namespace: valid.Namespace, Copier: testCopier{}}},
		{name: "When a complete CA is supplied, it should accept the options", options: Options{Name: valid.Name, Namespace: valid.Namespace, CopierImage: valid.CopierImage, CA: &KeyPair{CertPEM: []byte("cert"), KeyPEM: []byte("key")}}},
		{name: "When the name is not a DNS-1123 label, it should report the name problem", options: Options{Name: "Mirror_Registry", Namespace: valid.Namespace, CopierImage: valid.CopierImage}, wantErr: true, want: []string{"Name", "DNS-1123"}},
		{name: "When the namespace is not a DNS-1123 label, it should report the namespace problem", options: Options{Name: valid.Name, Namespace: "Bad Namespace", CopierImage: valid.CopierImage}, wantErr: true, want: []string{"Namespace", "DNS-1123"}},
		{name: "When the copier image is missing, it should require an image", options: Options{Name: valid.Name, Namespace: valid.Namespace}, wantErr: true, want: []string{"CopierImage"}},
		{name: "When the supplied CA certificate is empty, it should report the certificate problem", options: Options{Name: valid.Name, Namespace: valid.Namespace, CopierImage: valid.CopierImage, CA: &KeyPair{KeyPEM: []byte("key")}}, wantErr: true, want: []string{"CA.CertPEM"}},
		{name: "When the supplied CA key is empty, it should report the key problem", options: Options{Name: valid.Name, Namespace: valid.Namespace, CopierImage: valid.CopierImage, CA: &KeyPair{CertPEM: []byte("cert")}}, wantErr: true, want: []string{"CA.KeyPEM"}},
		{name: "When every required value is invalid, it should return all problems", options: Options{Name: "bad_name", Namespace: "bad namespace", CA: &KeyPair{}}, wantErr: true, want: []string{`Name "bad_name"`, `Namespace "bad namespace"`, "CopierImage", "CA.CertPEM", "CA.KeyPEM"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.options.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() error = %v, want error %v", err, tt.wantErr)
			}
			if err != nil {
				for _, expected := range tt.want {
					if !strings.Contains(err.Error(), expected) {
						t.Errorf("validate() error %q does not contain %q", err, expected)
					}
				}
			}
		})
	}
}

type testCopier struct{}

func (testCopier) Copy(context.Context, CopyRequest) (*CopyResult, error) { return nil, nil }

func TestRouteHost(t *testing.T) {
	label63 := strings.Repeat("a", 61) + "-b"
	tests := []struct {
		name        string
		registry    string
		namespace   string
		domain      string
		want        string
		wantErrText string
	}{
		{name: "When the first label is 63 characters, it should build the route host", registry: strings.Repeat("a", 61), namespace: "b", domain: "apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", want: label63 + ".apps.alesross-mc.alesross.hypershift.devcluster.openshift.com"},
		{name: "When the first label exceeds 63 characters, it should return an error", registry: strings.Repeat("a", 62), namespace: "b", domain: "apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", wantErrText: "exceeds 63"},
		{name: "When the ingress domain is empty, it should return an error", registry: "mirror-registry", namespace: "test-mirror-registry", wantErrText: "domain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := routeHost(tt.registry, tt.namespace, tt.domain)
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("routeHost() error = %v, want text %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("routeHost() = %q, %v; want %q, nil", got, err, tt.want)
			}
		})
	}
}

func TestInternalHost(t *testing.T) {
	tests := []struct {
		name      string
		service   string
		namespace string
		want      string
	}{
		{name: "When given the registry service, it should return the service DNS name and port", service: "mirror-registry", namespace: "test-mirror-registry", want: "mirror-registry.test-mirror-registry.svc:5000"},
		{name: "When given another service name, it should use the same registry port", service: "image-cache", namespace: "clusters", want: "image-cache.clusters.svc:5000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := internalHost(tt.service, tt.namespace); got != tt.want {
				t.Fatalf("internalHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServiceDNSNames(t *testing.T) {
	tests := []struct {
		name      string
		service   string
		namespace string
		want      []string
	}{
		{name: "When given the registry service, it should return all supported service DNS names", service: "mirror-registry", namespace: "test-mirror-registry", want: []string{"mirror-registry", "mirror-registry.test-mirror-registry", "mirror-registry.test-mirror-registry.svc", "mirror-registry.test-mirror-registry.svc.cluster.local"}},
		{name: "When given another namespace, it should use that namespace in qualified names", service: "image-cache", namespace: "clusters", want: []string{"image-cache", "image-cache.clusters", "image-cache.clusters.svc", "image-cache.clusters.svc.cluster.local"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serviceDNSNames(tt.service, tt.namespace)
			if len(got) != len(tt.want) {
				t.Fatalf("serviceDNSNames() = %#v, want %#v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("serviceDNSNames() = %#v, want %#v", got, tt.want)
				}
			}
		})
	}
}

func TestMirrorRepository(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		source      string
		want        string
		wantErrText string
	}{
		{name: "When source uses a registry port, it should replace the colon without IPv6 syntax", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "registry.build.ci.openshift.org:5000/ocp/release", want: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com/registry.build.ci.openshift.org-5000/ocp/release"},
		{name: "When source uses a standard registry, it should preserve registry and path", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "quay.io/openshift-release-dev/ocp-release", want: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com/quay.io/openshift-release-dev/ocp-release"},
		{name: "When source uses localhost as its registry, it should preserve the local registry host", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "localhost/openshift/release", want: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com/localhost/openshift/release"},
		{name: "When source uses localhost with a registry port, it should replace the colon", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "localhost:5000/openshift/release", want: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com/localhost-5000/openshift/release"},
		{name: "When source has no registry host, it should return an error", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "openshift/release", wantErrText: "no registry host"},
		{name: "When source has no repository path, it should return an error", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "quay.io", wantErrText: "registry host and path"},
		{name: "When source starts with an empty registry component, it should return an error", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "/openshift/release", wantErrText: "registry host and path"},
		{name: "When source has an empty path component, it should return a distinct error", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "quay.io/openshift//release", wantErrText: "has an empty path component"},
		{name: "When source registry uses IPv6, it should return an error", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "[2001:db8::1]/openshift/release", wantErrText: "IPv6"},
		{name: "When source contains a tag, it should return a distinct error", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "quay.io/openshift/release:4.21.10", wantErrText: "must not contain a tag"},
		{name: "When source contains a digest, it should return an error", host: "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", source: "quay.io/openshift/release@sha256:abc", wantErrText: "digest"},
		{name: "When the destination host is empty, it should return an error", source: "quay.io/openshift/release", wantErrText: "host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mirrorRepository(tt.host, tt.source)
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("mirrorRepository() error = %v, want text %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("mirrorRepository() = %q, %v; want %q, nil", got, err, tt.want)
			}
		})
	}
}

func TestSplitDigestRef(t *testing.T) {
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name        string
		ref         string
		wantRepo    string
		wantDigest  string
		wantErrText string
	}{
		{name: "When reference has a sha256 digest, it should split repository and digest", ref: "quay.io/openshift-release-dev/ocp-release@" + digest, wantRepo: "quay.io/openshift-release-dev/ocp-release", wantDigest: digest},
		{name: "When reference has no digest, it should return an error", ref: "quay.io/openshift-release-dev/ocp-release:4.21.10", wantErrText: "separator"},
		{name: "When digest uses another algorithm, it should return an error", ref: "quay.io/openshift-release-dev/ocp-release@sha512:012345", wantErrText: "sha256"},
		{name: "When digest has fewer than 64 hexadecimal characters, it should return an error", ref: "quay.io/openshift-release-dev/ocp-release@sha256:0123", wantErrText: "sha256"},
		{name: "When digest contains non-hexadecimal characters, it should return an error", ref: "quay.io/openshift-release-dev/ocp-release@sha256:" + strings.Repeat("z", 64), wantErrText: "sha256"},
		{name: "When digest contains uppercase hexadecimal characters, it should return an error", ref: "quay.io/openshift-release-dev/ocp-release@sha256:" + strings.Repeat("A", 64), wantErrText: "sha256"},
		{name: "When repository is empty, it should return an error", ref: "@" + digest, wantErrText: "sha256"},
		{name: "When reference has multiple digest separators, it should return an error", ref: "quay.io/repo@sha256:" + strings.Repeat("a", 64) + "@sha256:" + strings.Repeat("b", 64), wantErrText: "exactly one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository, gotDigest, err := splitDigestRef(tt.ref)
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("splitDigestRef() error = %v, want text %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil || repository != tt.wantRepo || gotDigest != tt.wantDigest {
				t.Fatalf("splitDigestRef() = (%q, %q, %v), want (%q, %q, nil)", repository, gotDigest, err, tt.wantRepo, tt.wantDigest)
			}
		})
	}
}

func TestNew(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	tests := []struct {
		name    string
		options Options
		wantErr bool
	}{
		{name: "When valid options are provided, it should return a registry with defaulted options", options: Options{CopierImage: "quay.io/openshift/cli:latest"}},
		{name: "When validation fails, it should return an error without a registry", options: Options{Namespace: "bad namespace", CopierImage: "quay.io/openshift/cli:latest"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(client, tt.options)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if got != nil {
					t.Fatalf("New() returned registry %#v on validation error", got)
				}
				return
			}
			if got == nil || got.client != client || got.opts.Namespace != DefaultNamespace || got.opts.Name != DefaultName || got.opts.Image != DefaultRegistryImage || !got.opts.Storage.Size.Equal(resource.MustParse(DefaultStorageSize)) || got.opts.CopierImage != tt.options.CopierImage {
				t.Fatalf("New() registry = %#v, want client %p and defaulted options", got, client)
			}
		})
	}
}
