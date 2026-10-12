package mirrorregistry

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	registryTestDigest  = "sha256:e642e8604d305a3b82c8c1807b5df7a1a84cc650d57a60f9c5c2b78efec54b3f"
	registryTestImage   = "quay.io/libpod/registry@" + registryTestDigest
	registryTestRelease = "quay.io/openshift-release-dev/ocp-release@" + registryTestDigest
	registryTestVirtual = "mirror-source.hypershift-e2e.invalid/libpod/registry"
	registryTestHost    = "mirror-registry-test-mirror-registry.apps.alesross-mc.alesross.hypershift.devcluster.openshift.com"
)

func TestBuildCopyRequest(t *testing.T) {
	opts := Options{}.withDefaults()
	ca, secret := []byte("registry-ca"), []byte(`{"auths":{}}`)
	internal := "mirror-registry.test-mirror-registry.svc:5000"
	tests := []struct {
		name    string
		req     MirrorRequest
		want    CopyRequest
		wantErr bool
	}{
		{
			name: "When images share a virtual source and a release is requested, it should target internal repositories in request order",
			req:  MirrorRequest{Images: []ImageMapping{{From: registryTestImage, SourceRepository: registryTestVirtual}, {From: registryTestRelease, SourceRepository: registryTestVirtual}, {From: registryTestImage}}, ReleaseImages: []string{registryTestRelease}, SourcePullSecret: secret},
			want: CopyRequest{Namespace: DefaultNamespace, CABundlePEM: ca, SourcePullSecret: secret,
				Images:   []ImageCopy{{From: registryTestImage, To: internal + "/" + registryTestVirtual}, {From: registryTestRelease, To: internal + "/" + registryTestVirtual}, {From: registryTestImage, To: internal + "/quay.io/libpod/registry"}},
				Releases: []ReleaseCopy{{From: registryTestRelease, ToRepository: internal + "/quay.io/openshift-release-dev/ocp-release"}}},
		},
		{name: "When the request is empty, it should preserve namespace and CA", want: CopyRequest{Namespace: DefaultNamespace, CABundlePEM: ca}},
		{name: "When an image has a tag instead of a digest, it should return an error", req: MirrorRequest{Images: []ImageMapping{{From: "quay.io/libpod/registry:2.8.2"}}}, wantErr: true},
		{name: "When a release has a tag instead of a digest, it should return an error", req: MirrorRequest{ReleaseImages: []string{"quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64"}}, wantErr: true},
		{name: "When the virtual source contains a tag, it should return an error", req: MirrorRequest{Images: []ImageMapping{{From: registryTestImage, SourceRepository: registryTestVirtual + ":2.8.2"}}}, wantErr: true},
		{name: "When a release repository has no registry host, it should return an error", req: MirrorRequest{ReleaseImages: []string{"ocp-release@" + registryTestDigest}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildCopyRequest(opts, tt.req, ca)
			if (err != nil) != tt.wantErr {
				t.Fatalf("buildCopyRequest() error = %v, want error %t", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("buildCopyRequest() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestBuildResult(t *testing.T) {
	opts := Options{}.withDefaults()
	ca := []byte("registry-ca")
	req := MirrorRequest{Images: []ImageMapping{{From: registryTestImage, SourceRepository: registryTestVirtual}, {From: registryTestRelease, SourceRepository: registryTestVirtual}, {From: registryTestImage}}, ReleaseImages: []string{registryTestRelease}}
	copyRes := &CopyResult{ReleaseSourceRepositories: map[string][]string{registryTestRelease: {"quay.io/openshift-release-dev/ocp-release", "quay.io/libpod/registry", "quay.io/libpod/registry"}}}
	want := &Result{Host: registryTestHost, InternalHost: "mirror-registry.test-mirror-registry.svc:5000", CABundlePEM: ca,
		Images: []MirroredImage{
			{From: registryTestImage, SourceRef: registryTestVirtual + "@" + registryTestDigest, MirrorRef: registryTestHost + "/" + registryTestVirtual + "@" + registryTestDigest},
			{From: registryTestRelease, SourceRef: registryTestVirtual + "@" + registryTestDigest, MirrorRef: registryTestHost + "/" + registryTestVirtual + "@" + registryTestDigest},
			{From: registryTestImage, SourceRef: registryTestImage, MirrorRef: registryTestHost + "/quay.io/libpod/registry@" + registryTestDigest},
		},
		ImageContentSources: []hyperv1.ImageContentSource{
			{Source: registryTestVirtual, Mirrors: []string{registryTestHost + "/" + registryTestVirtual}},
			{Source: "quay.io/libpod/registry", Mirrors: []string{registryTestHost + "/quay.io/libpod/registry", registryTestHost + "/quay.io/openshift-release-dev/ocp-release"}},
			{Source: "quay.io/openshift-release-dev/ocp-release", Mirrors: []string{registryTestHost + "/quay.io/openshift-release-dev/ocp-release"}},
		},
	}
	tests := []struct {
		name    string
		req     MirrorRequest
		copyRes *CopyResult
		want    *Result
		wantErr bool
	}{
		{name: "When images and release sources overlap, it should merge deduplicated sorted public mirrors and preserve image order", req: req, copyRes: copyRes, want: want},
		{name: "When no content is requested, it should return endpoint metadata without a copy result", want: &Result{Host: registryTestHost, InternalHost: want.InternalHost, CABundlePEM: ca}},
		{name: "When a requested release is missing from the copy result, it should return an error", req: req, copyRes: &CopyResult{}, wantErr: true},
		{name: "When releases are requested and the copy result is nil, it should return an error", req: req, wantErr: true},
		{name: "When an image is not by digest, it should return an error", req: MirrorRequest{Images: []ImageMapping{{From: "quay.io/libpod/registry:2.8.2"}}}, wantErr: true},
		{name: "When a release is not by digest, it should return an error", req: MirrorRequest{ReleaseImages: []string{"quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64"}}, copyRes: copyRes, wantErr: true},
		{name: "When a virtual source has a tag, it should return an error", req: MirrorRequest{Images: []ImageMapping{{From: registryTestImage, SourceRepository: registryTestVirtual + ":2.8.2"}}}, wantErr: true},
		{name: "When a release repository lacks a registry host, it should return an error", req: MirrorRequest{ReleaseImages: []string{"ocp-release@" + registryTestDigest}}, copyRes: copyRes, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildResult(opts, registryTestHost, ca, tt.req, tt.copyRes)
			if (err != nil) != tt.wantErr {
				t.Fatalf("buildResult() error = %v, want error %t", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("buildResult() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func registryTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, appsv1.AddToScheme, routev1.AddToScheme, configv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func TestResolveCA(t *testing.T) {
	storedCA, _ := testRegistryTLSMaterial(t)
	callerCA, _ := testRegistryTLSMaterial(t)
	expiredCA := newTestCA(t, -time.Hour)
	readErr := apierrors.NewForbidden(corev1.Resource("secrets"), DefaultName+"-ca", errors.New("CA Secret access denied"))
	tests := []struct {
		name    string
		stored  *KeyPair
		caller  *KeyPair
		want    *KeyPair
		readErr error
		wantErr string
	}{
		{name: "When the caller supplies a CA and one is stored, it should prefer the caller and retain the stored CA", stored: &storedCA, caller: &callerCA, want: &callerCA},
		{name: "When only a stored CA exists, it should reuse it", stored: &storedCA, want: &storedCA},
		{name: "When the CA Secret and namespace are absent, it should generate a verifiable CA"},
		{name: "When the caller supplies a CA on first use, it should use it", caller: &callerCA, want: &callerCA},
		{name: "When the stored certificate is missing, it should return an error", stored: &KeyPair{KeyPEM: storedCA.KeyPEM}, wantErr: "validate stored CA Secret"},
		{name: "When the stored private key is missing, it should return an error", stored: &KeyPair{CertPEM: storedCA.CertPEM}, wantErr: "validate stored CA Secret"},
		{name: "When the stored certificate is malformed, it should return an error", stored: &KeyPair{CertPEM: []byte("malformed"), KeyPEM: storedCA.KeyPEM}, wantErr: "validate stored CA Secret"},
		{name: "When the stored key is malformed despite a caller CA, it should return an error without rotating", stored: &KeyPair{CertPEM: storedCA.CertPEM, KeyPEM: []byte("malformed")}, caller: &callerCA, wantErr: "validate stored CA Secret"},
		{name: "When the stored key does not match the certificate, it should return an error", stored: &KeyPair{CertPEM: storedCA.CertPEM, KeyPEM: callerCA.KeyPEM}, wantErr: "validate stored CA Secret"},
		{name: "When a stored CA has expired despite a caller CA, it should explain how to rotate it", stored: &expiredCA, caller: &callerCA, wantErr: "not currently valid"},
		{name: "When reading the CA Secret is forbidden, it should propagate the error", readErr: readErr, wantErr: "read CA Secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := Options{CA: tt.caller}.withDefaults()
			builder := fake.NewClientBuilder().WithScheme(registryTestScheme(t))
			if tt.stored != nil {
				builder = builder.WithObjects(buildCASecret(opts, *tt.stored))
			}
			if tt.readErr != nil {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, crclient.WithWatch, crclient.ObjectKey, crclient.Object, ...crclient.GetOption) error {
						return tt.readErr
					},
				})
			}
			r := &Registry{client: builder.Build(), opts: opts}
			resolved, stored, err := r.resolveCA(t.Context())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveCA() error = %v, want %q", err, tt.wantErr)
				}
				if tt.stored != nil && !strings.Contains(err.Error(), "delete the Secret or its namespace to rotate the CA") {
					t.Fatalf("resolveCA() did not explain recovery from an invalid stored CA: %v", err)
				}
				if tt.readErr != nil && !errors.Is(err, tt.readErr) {
					t.Fatalf("resolveCA() did not wrap read error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored, tt.stored) {
				t.Fatal("resolveCA() did not preserve the stored CA")
			}
			if tt.want != nil && !reflect.DeepEqual(resolved, *tt.want) {
				t.Fatal("resolveCA() did not select the expected CA")
			}
			_, certificate, err := parseCA(resolved)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(certificate)
			if _, err := certificate.Verify(x509.VerifyOptions{Roots: roots}); err != nil {
				t.Fatalf("resolved CA does not verify: %v", err)
			}
		})
	}
}

func TestTeardown(t *testing.T) {
	tests := []struct {
		name    string
		present bool
	}{
		{name: "When the namespace is absent, it should succeed"},
		{name: "When the namespace exists, it should delete it and succeed on a repeated call", present: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(registryTestScheme(t)).Build()
			r, err := New(client, Options{Copier: testCopier{}})
			if err != nil {
				t.Fatal(err)
			}
			namespace := buildNamespace(r.opts)
			if tt.present {
				if err := client.Create(t.Context(), namespace); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Teardown(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(namespace), &corev1.Namespace{}); !apierrors.IsNotFound(err) {
				t.Fatalf("Namespace still exists: %v", err)
			}
			if err := r.Teardown(t.Context()); err != nil {
				t.Fatalf("repeated Teardown(): %v", err)
			}
		})
	}
}

func TestConfigure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		readOnly    bool
		readFailure bool
		persistent  bool
	}{
		{name: "When a writable-stage retry has new TLS material, it should change the serving hash even with identical configuration"},
		{name: "When copying has completed, it should install read-only configuration and its hash", readOnly: true},
		{name: "When a rollout read is throttled once, it should retry and complete the rollout", readFailure: true},
		{name: "When rollout reads remain throttled, it should report the last API error and context deadline", readFailure: true, persistent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := Options{}.withDefaults()
			config := buildConfigMap(opts, false)
			oldHash := fmt.Sprintf("%x", sha256.Sum256([]byte(config.Data["config.yml"])))
			deployment := buildDeployment(opts, oldHash)
			deployment.Spec.Template.Annotations[servingHashAnnotation] = "previous-serving-hash"
			// Supply already-ready status: controller-dependent waiting is covered by T6.
			deployment.Status = appsv1.DeploymentStatus{UpdatedReplicas: 1, AvailableReplicas: 1, Replicas: 1}
			readErr := apierrors.NewTooManyRequests("rollout read throttled", 1)
			reads := 0
			client := fake.NewClientBuilder().WithScheme(registryTestScheme(t)).WithStatusSubresource(deployment).WithObjects(config, deployment).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
					if _, ok := obj.(*appsv1.Deployment); ok {
						reads++
						// The first read is reconciliation; inject errors only in the rollout poll.
						if tt.readFailure && (reads == 2 || (tt.persistent && reads > 2)) {
							return readErr
						}
					}
					return client.Get(ctx, key, obj, opts...)
				},
			}).Build()
			r := &Registry{client: client, opts: opts}
			serving := KeyPair{CertPEM: []byte("new-serving-certificate")}
			timeout := 30 * time.Second
			if tt.persistent {
				timeout = time.Second
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			err := r.configure(ctx, tt.readOnly, serving)
			if tt.persistent {
				assertRegistryPollError(t, err, readErr, "Deployment "+opts.Namespace+"/"+opts.Name)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.readFailure && reads < 3 {
				t.Fatalf("Deployment reads = %d, want reconciliation followed by failed and successful rollout reads", reads)
			}
			if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if config.Data["config.yml"] != buildConfigMap(opts, tt.readOnly).Data["config.yml"] {
				t.Fatal("configure() did not install the requested configuration")
			}
			if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(deployment), deployment); err != nil {
				t.Fatal(err)
			}
			annotations := deployment.Spec.Template.Annotations
			if annotations[configHashAnnotation] != fmt.Sprintf("%x", sha256.Sum256([]byte(config.Data["config.yml"]))) {
				t.Fatal("configure() Deployment config hash does not match config.yml")
			}
			if annotations[servingHashAnnotation] != fmt.Sprintf("%x", sha256.Sum256(serving.CertPEM)) {
				t.Fatal("configure() Deployment serving hash does not match the new TLS material")
			}
			if !tt.readOnly && annotations[configHashAnnotation] != oldHash {
				t.Fatal("writable-stage retry unexpectedly changed the configuration hash")
			}
		})
	}
	t.Run("When rollout reads recover but the Deployment is not ready, it should omit the stale API error", func(t *testing.T) {
		t.Parallel()
		opts := Options{}.withDefaults()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		deployment := buildDeployment(opts, "previous-config-hash")
		// Skip the reconciliation read; fail the first rollout read and cancel
		// after the next successful read of this not-yet-ready Deployment.
		client, readErr := registryRecoveredReadClient(t, deployment, 1, cancel)
		r := &Registry{client: client, opts: opts}
		err := r.configure(ctx, false, KeyPair{CertPEM: []byte("new-serving-certificate")})
		assertRegistryRecoveredReadError(t, err, readErr)
	})
}

func assertRegistryPollError(t *testing.T, err, readErr error, object string) {
	t.Helper()
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, readErr) {
		t.Fatalf("poll error = %v, want context deadline and last API error %v", err, readErr)
	}
	if !strings.Contains(err.Error(), object) {
		t.Fatalf("poll error = %v, want object %q", err, object)
	}
}

func TestWaitGone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		persistent bool
	}{
		{name: "When a deletion read fails once, it should retry until the object is NotFound"},
		{name: "When deletion reads keep failing, it should report the last API error and wait cap", persistent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := Options{}.withDefaults()
			namespace := buildNamespace(opts)
			readErr := apierrors.NewServiceUnavailable("namespace deletion read unavailable")
			reads := 0
			client := fake.NewClientBuilder().WithScheme(registryTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
					reads++
					if reads == 1 || tt.persistent {
						return readErr
					}
					return client.Get(ctx, key, obj, opts...)
				},
			}).Build()
			r := &Registry{client: client, opts: opts}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cap := 20 * time.Second
			if tt.persistent {
				cap = time.Second
			}
			err := r.waitGone(ctx, namespace, cap, "test namespace deletion")
			if tt.persistent {
				assertRegistryPollError(t, err, readErr, opts.Namespace)
				if ctx.Err() != nil {
					t.Fatal("waitGone() exceeded its cap and consumed the parent context deadline")
				}
				return
			}
			if err != nil || reads != 2 {
				t.Fatalf("waitGone() = %v after %d reads, want success after transient failure and NotFound", err, reads)
			}
		})
	}
	t.Run("When deletion reads recover but the object remains present, it should omit the stale API error", func(t *testing.T) {
		t.Parallel()
		opts := Options{}.withDefaults()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		namespace := buildNamespace(opts)
		client, readErr := registryRecoveredReadClient(t, namespace, 0, cancel)
		r := &Registry{client: client, opts: opts}
		err := r.waitGone(ctx, namespace, 20*time.Second, "test namespace deletion")
		assertRegistryRecoveredReadError(t, err, readErr)
	})
}

func TestWaitAdmitted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		persistent bool
		missing    bool
	}{
		{name: "When an admission read fails once, it should retry and observe the admitted Route"},
		{name: "When admission reads keep failing, it should report the last API error and context deadline", persistent: true},
		{name: "When the Route is missing, it should return NotFound without retrying", missing: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := Options{}.withDefaults()
			route := buildRoute(opts, registryTestHost)
			route.Status.Ingress = []routev1.RouteIngress{{Conditions: []routev1.RouteIngressCondition{{Type: routev1.RouteAdmitted, Status: corev1.ConditionTrue}}}}
			readErr := apierrors.NewTimeoutError("Route admission read timed out", 1)
			reads := 0
			builder := fake.NewClientBuilder().WithScheme(registryTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
					reads++
					if !tt.missing && (reads == 1 || tt.persistent) {
						return readErr
					}
					return client.Get(ctx, key, obj, opts...)
				},
			})
			if !tt.missing {
				builder = builder.WithObjects(route)
			}
			r := &Registry{client: builder.Build(), opts: opts}
			timeout := 30 * time.Second
			if tt.persistent {
				timeout = time.Second
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			err := r.waitAdmitted(ctx, route)
			if tt.persistent {
				assertRegistryPollError(t, err, readErr, "Route "+opts.Namespace+"/"+opts.Name)
				return
			}
			if tt.missing {
				if !apierrors.IsNotFound(err) || reads != 1 {
					t.Fatalf("waitAdmitted() = %v after %d reads, want immediate NotFound", err, reads)
				}
				return
			}
			if err != nil || reads != 2 {
				t.Fatalf("waitAdmitted() = %v after %d reads, want successful admission after retry", err, reads)
			}
		})
	}
	t.Run("When admission reads recover but the Route is not admitted, it should omit the stale API error", func(t *testing.T) {
		t.Parallel()
		opts := Options{}.withDefaults()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		route := buildRoute(opts, registryTestHost)
		client, readErr := registryRecoveredReadClient(t, route, 0, cancel)
		r := &Registry{client: client, opts: opts}
		err := r.waitAdmitted(ctx, route)
		assertRegistryRecoveredReadError(t, err, readErr)
	})
}

// registryRecoveredReadClient fails one read, then cancels the wait after a
// successful read of a still-pending object. This isolates stale-error reporting
// from the controller-dependent wait and avoids another full poll interval.
func registryRecoveredReadClient(t *testing.T, obj crclient.Object, skipReads int, cancel context.CancelFunc) (crclient.Client, error) {
	t.Helper()
	readErr := apierrors.NewServiceUnavailable("transient read failure before recovery")
	reads := 0
	client := fake.NewClientBuilder().WithScheme(registryTestScheme(t)).WithStatusSubresource(obj).WithObjects(obj).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, target crclient.Object, opts ...crclient.GetOption) error {
			if reflect.TypeOf(target) != reflect.TypeOf(obj) || key != crclient.ObjectKeyFromObject(obj) {
				return client.Get(ctx, key, target, opts...)
			}
			reads++
			if reads == skipReads+1 {
				return readErr
			}
			err := client.Get(ctx, key, target, opts...)
			if err == nil && reads == skipReads+2 {
				cancel()
			}
			return err
		},
	}).Build()
	return client, readErr
}

func assertRegistryRecoveredReadError(t *testing.T, err, readErr error) {
	t.Helper()
	if !errors.Is(err, context.Canceled) || errors.Is(err, readErr) {
		t.Fatalf("poll error = %v, want context cancellation without stale API error %v", err, readErr)
	}
}

type registryRoundTripper func(*http.Request) (*http.Response, error)

func (f registryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestClientForWithdrawal(t *testing.T) {
	resolved, _ := testRegistryTLSMaterial(t)
	oldCA, _ := testRegistryTLSMaterial(t)
	tests := []struct {
		name   string
		stored *KeyPair
		clone  bool
	}{
		{name: "When no CA is stored, it should reuse the resolved CA client"},
		{name: "When the stored and resolved CAs match, it should reuse the existing connection pool", stored: &resolved},
		{name: "When the caller replaces the stored CA, it should clone the client and trust only the old CA", stored: &oldCA, clone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roots := x509.NewCertPool()
			roots.AppendCertsFromPEM(resolved.CertPEM)
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}
			got := clientForWithdrawal(client, transport, resolved, tt.stored)
			defer got.CloseIdleConnections()
			if (got != client) != tt.clone {
				t.Fatalf("clientForWithdrawal() cloned client = %t, want %t", got != client, tt.clone)
			}
			if !tt.clone {
				return
			}
			gotTransport, ok := got.Transport.(*http.Transport)
			if !ok || gotTransport == transport || gotTransport.TLSClientConfig == transport.TLSClientConfig {
				t.Fatal("withdrawal client must have an independent transport and TLS config")
			}
			wantRoots := x509.NewCertPool()
			wantRoots.AppendCertsFromPEM(oldCA.CertPEM)
			wantResolvedRoots := x509.NewCertPool()
			wantResolvedRoots.AppendCertsFromPEM(resolved.CertPEM)
			if !gotTransport.TLSClientConfig.RootCAs.Equal(wantRoots) || !transport.TLSClientConfig.RootCAs.Equal(wantResolvedRoots) {
				t.Fatal("withdrawal client must trust only the stored CA without changing the verification client's roots")
			}
			if got.Timeout != client.Timeout || gotTransport.TLSClientConfig.MinVersion != tls.VersionTLS12 || !errors.Is(got.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
				t.Fatal("withdrawal client did not preserve timeout, minimum TLS version and redirect policy")
			}
		})
	}
}

func TestDeExpose(t *testing.T) {
	tests := []struct {
		name    string
		present bool
	}{
		{name: "When no Route exists, it should skip the withdrawal request"},
		{name: "When a Route exists, it should delete it before checking routing withdrawal", present: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := Options{}.withDefaults()
			route := buildRoute(opts, registryTestHost)
			builder := fake.NewClientBuilder().WithScheme(registryTestScheme(t))
			if tt.present {
				builder = builder.WithObjects(route)
			}
			client := builder.Build()
			r := &Registry{client: client, opts: opts}
			calls := 0
			httpClient := &http.Client{Transport: registryRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if err := client.Get(req.Context(), crclient.ObjectKeyFromObject(route), &routev1.Route{}); !apierrors.IsNotFound(err) {
					t.Fatalf("Route exists during withdrawal check: %v", err)
				}
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
			})}
			if err := r.deExpose(t.Context(), httpClient, registryTestHost); err != nil {
				t.Fatal(err)
			}
			wantCalls := 0
			if tt.present {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("withdrawal requests = %d, want %d", calls, wantCalls)
			}
		})
	}
	t.Run("When a caller replaces the CA while the old endpoint still serves traffic, it should trust the stored CA until routing is withdrawn", func(t *testing.T) {
		opts := Options{}.withDefaults()
		storedCA, _ := testRegistryTLSMaterial(t)
		callerCA, _ := testRegistryTLSMaterial(t)
		opts.CA = &callerCA
		serving, err := servingCert(storedCA, opts.Name, []string{"localhost"})
		if err != nil {
			t.Fatal(err)
		}
		certificate, err := tls.X509KeyPair(serving.CertPEM, serving.KeyPEM)
		if err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			call := calls.Add(1)
			if req.URL.Path != "/v2/" {
				t.Errorf("withdrawal request path = %q, want /v2/", req.URL.Path)
			}
			if call == 1 {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
		server.StartTLS()
		defer server.Close()
		host := strings.Replace(strings.TrimPrefix(server.URL, "https://"), "127.0.0.1", "localhost", 1)
		route := buildRoute(opts, host)
		client := fake.NewClientBuilder().WithScheme(registryTestScheme(t)).WithObjects(route, buildCASecret(opts, storedCA)).Build()
		r := &Registry{client: client, opts: opts}
		resolved, stored, err := r.resolveCA(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if stored == nil || !reflect.DeepEqual(resolved, callerCA) {
			t.Fatal("resolveCA() must preserve the old CA while selecting the caller CA")
		}
		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM(resolved.CertPEM)
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
		defer transport.CloseIdleConnections()
		clientForVerify := &http.Client{Transport: transport, Timeout: 10 * time.Second}
		withdrawalClient := clientForWithdrawal(clientForVerify, transport, resolved, stored)
		defer withdrawalClient.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		if err := r.deExpose(ctx, withdrawalClient, host); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 2 {
			t.Fatalf("withdrawal requests = %d, want a verified HTTP 200 followed by HTTP 503", calls.Load())
		}
		if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(route), &routev1.Route{}); !apierrors.IsNotFound(err) {
			t.Fatalf("Route still exists after withdrawal: %v", err)
		}
	})
}

func TestSetup(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		ingress *configv1.Ingress
		stored  *KeyPair
		wantErr string
	}{
		{name: "When the cluster Ingress is absent, it should fail before changing resources", opts: Options{Copier: testCopier{}}, wantErr: "Ingress cluster"},
		{name: "When the ingress domain is empty, it should reject the Route host", opts: Options{Copier: testCopier{}}, ingress: &configv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}, wantErr: "ingress domain must not be empty"},
		{name: "When the supplied CA is malformed, it should fail before deleting the Route", opts: Options{Copier: testCopier{}, IngressDomain: "apps.alesross-mc.alesross.hypershift.devcluster.openshift.com", CA: &KeyPair{CertPEM: []byte("malformed"), KeyPEM: []byte("malformed")}}, wantErr: "prepare TLS"},
		{name: "When the stored CA is malformed, it should fail before deleting the Route", opts: Options{Copier: testCopier{}, IngressDomain: "apps.alesross-mc.alesross.hypershift.devcluster.openshift.com"}, stored: &KeyPair{CertPEM: []byte("malformed"), KeyPEM: []byte("malformed")}, wantErr: "validate stored CA Secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := buildRoute(tt.opts.withDefaults(), registryTestHost)
			builder := fake.NewClientBuilder().WithScheme(registryTestScheme(t)).WithObjects(route)
			if tt.ingress != nil {
				builder = builder.WithObjects(tt.ingress)
			}
			if tt.stored != nil {
				builder = builder.WithObjects(buildCASecret(tt.opts.withDefaults(), *tt.stored))
			}
			client := builder.Build()
			r, err := New(client, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			result, err := r.Setup(t.Context(), MirrorRequest{})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || result != nil {
				t.Fatalf("Setup() = %#v, %v; want error containing %q", result, err, tt.wantErr)
			}
			if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(route), &routev1.Route{}); err != nil {
				t.Fatalf("Route was changed on preparation failure: %v", err)
			}
		})
	}
	t.Run("When the context is canceled, it should return the context error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r := &Registry{client: fake.NewClientBuilder().WithScheme(registryTestScheme(t)).Build(), opts: Options{}.withDefaults()}
		_, err := r.Setup(ctx, MirrorRequest{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Setup() error = %v, want context cancellation", err)
		}
	})
}
