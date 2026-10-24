package mirrorregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	registryPollInterval  = 5 * time.Second
	servingHashAnnotation = "mirrorregistry.hypershift.io/serving-hash"
)

func (r *Registry) setup(ctx context.Context, req MirrorRequest) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("setup registry %s/%s: %w", r.opts.Namespace, r.opts.Name, err)
	}
	domain := r.opts.IngressDomain
	if domain == "" {
		ingress := &configv1.Ingress{}
		if err := r.client.Get(ctx, crclient.ObjectKey{Name: "cluster"}, ingress); err != nil {
			return nil, fmt.Errorf("resolve ingress domain from Ingress cluster: %w", err)
		}
		domain = ingress.Spec.Domain
	}
	host, err := routeHost(r.opts.Name, r.opts.Namespace, domain)
	if err != nil {
		return nil, fmt.Errorf("resolve Route %s/%s host: %w", r.opts.Namespace, r.opts.Name, err)
	}
	ca, stored, err := r.resolveCA(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve CA for registry %s/%s: %w", r.opts.Namespace, r.opts.Name, err)
	}
	// Reject an invalid request before withdrawing the Route of a working
	// mirror or making it writable.
	copyReq, err := buildCopyRequest(r.opts, req, ca.CertPEM)
	if err != nil {
		return nil, fmt.Errorf("build copy request for registry %s/%s: %w", r.opts.Namespace, r.opts.Name, err)
	}
	tlsOpts := r.opts
	tlsOpts.CA = &ca
	_, serving, err := tlsMaterial(tlsOpts, host)
	if err != nil {
		return nil, fmt.Errorf("prepare TLS for registry %s/%s: %w", r.opts.Namespace, r.opts.Name, err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.CertPEM) {
		return nil, fmt.Errorf("prepare TLS client for registry %s/%s: invalid CA", r.opts.Namespace, r.opts.Name)
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		// A redirect is not a successful response from the registry endpoint.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	withdrawalClient := clientForWithdrawal(httpClient, transport, ca, stored)
	defer withdrawalClient.CloseIdleConnections()
	if err := r.deExpose(ctx, withdrawalClient, host); err != nil {
		return nil, err
	}
	namespace := buildNamespace(r.opts)
	if err := r.client.Get(ctx, crclient.ObjectKeyFromObject(namespace), namespace); err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("read Namespace %s before setup: %w", namespace.Name, err)
	}
	if namespace.DeletionTimestamp != nil {
		if err := r.waitGone(ctx, namespace, 10*time.Minute, "wait for terminating namespace"); err != nil {
			return nil, err
		}
	}
	if err := r.reconcile(ctx, buildNamespace(r.opts)); err != nil {
		return nil, err
	}
	for _, obj := range []crclient.Object{buildCASecret(r.opts, ca), buildTLSSecret(r.opts, serving), buildPVC(r.opts), buildService(r.opts)} {
		if err := r.reconcile(ctx, obj); err != nil {
			return nil, err
		}
	}
	if err := r.configure(ctx, false, serving); err != nil {
		return nil, err
	}
	copyRes, err := r.opts.Copier.Copy(ctx, copyReq)
	if err != nil {
		return nil, fmt.Errorf("copy into registry %s/%s: %w", r.opts.Namespace, r.opts.Name, err)
	}
	if err := r.configure(ctx, true, serving); err != nil {
		return nil, err
	}
	route := buildRoute(r.opts, host)
	if err := r.reconcile(ctx, route); err != nil {
		return nil, err
	}
	if err := r.waitAdmitted(ctx, route); err != nil {
		return nil, err
	}
	if err := waitEndpoint(ctx, httpClient, host, true, r.pollInterval); err != nil {
		return nil, err
	}
	result, err := buildResult(r.opts, host, ca.CertPEM, req, copyRes)
	if err != nil {
		return nil, fmt.Errorf("build result for registry %s/%s: %w", r.opts.Namespace, r.opts.Name, err)
	}
	return result, nil
}

func (r *Registry) resolveCA(ctx context.Context) (resolved KeyPair, stored *KeyPair, err error) {
	secret := &corev1.Secret{}
	key := crclient.ObjectKey{Namespace: r.opts.Namespace, Name: r.opts.Name + "-ca"}
	if err := r.client.Get(ctx, key, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			return KeyPair{}, nil, fmt.Errorf("read CA Secret %s: %w", key, err)
		}
	} else {
		ca := KeyPair{CertPEM: secret.Data["ca.crt"], KeyPEM: secret.Data["ca.key"]}
		if _, _, err := parseCA(ca); err != nil {
			return KeyPair{}, nil, fmt.Errorf("validate stored CA Secret %s (delete the Secret or its namespace to rotate the CA; consumers must trust the replacement): %w", key, err)
		}
		stored = &ca
	}
	if r.opts.CA != nil {
		return *r.opts.CA, stored, nil
	}
	if stored != nil {
		return *stored, stored, nil
	}
	resolved, err = generateCA()
	return resolved, nil, err
}

// clientForWithdrawal receives already-validated CAs. A running pod still
// serves a certificate signed by stored, even if this Setup replaces the CA.
func clientForWithdrawal(client *http.Client, transport *http.Transport, resolved KeyPair, stored *KeyPair) *http.Client {
	if stored == nil || bytes.Equal(stored.CertPEM, resolved.CertPEM) {
		return client
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(stored.CertPEM)
	withdrawalTransport := transport.Clone()
	withdrawalTransport.TLSClientConfig.RootCAs = roots
	clientCopy := *client
	clientCopy.Transport = withdrawalTransport
	return &clientCopy
}

func (r *Registry) deExpose(ctx context.Context, httpClient *http.Client, host string) error {
	route := buildRoute(r.opts, host)
	if err := r.client.Get(ctx, crclient.ObjectKeyFromObject(route), route); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("de-expose Route %s/%s: %w", route.Namespace, route.Name, err)
	}
	if err := r.client.Delete(ctx, route); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete Route %s/%s before enabling writes: %w", route.Namespace, route.Name, err)
	}
	if err := r.waitGone(ctx, route, 2*time.Minute, "de-expose Route"); err != nil {
		return err
	}
	return waitEndpoint(ctx, httpClient, host, false, r.pollInterval)
}

func (r *Registry) reconcile(ctx context.Context, desired crclient.Object) error {
	obj := desired.DeepCopyObject().(crclient.Object)
	_, err := upsert.New(false).CreateOrUpdate(ctx, r.client, obj, func() error {
		labels := maps.Clone(obj.GetLabels())
		if labels == nil {
			labels = map[string]string{}
		}
		maps.Copy(labels, desired.GetLabels())
		obj.SetLabels(labels)
		annotations := maps.Clone(obj.GetAnnotations())
		if annotations == nil {
			annotations = map[string]string{}
		}
		maps.Copy(annotations, desired.GetAnnotations())
		obj.SetAnnotations(annotations)
		switch target := obj.(type) {
		case *corev1.Namespace:
			// Keep the namespace's server-managed spec and finalizers.
		case *corev1.Secret:
			want := desired.(*corev1.Secret)
			target.Type, target.Data = want.Type, want.Data
		case *corev1.PersistentVolumeClaim:
			want := desired.(*corev1.PersistentVolumeClaim)
			target.Spec.AccessModes = want.Spec.AccessModes
			target.Spec.Resources = want.Spec.Resources
			if want.Spec.StorageClassName != nil {
				target.Spec.StorageClassName = want.Spec.StorageClassName
			}
			// Keep binding and defaulted storage class/volume mode on re-runs.
		case *corev1.Service:
			target.Spec = desired.(*corev1.Service).Spec
		case *corev1.ConfigMap:
			target.Data = desired.(*corev1.ConfigMap).Data
		case *appsv1.Deployment:
			target.Spec = desired.(*appsv1.Deployment).Spec
		case *routev1.Route:
			target.Spec = desired.(*routev1.Route).Spec
		default:
			return fmt.Errorf("unsupported registry resource %T", obj)
		}
		// upsert preserves server defaults for Services, Deployments and Routes.
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile %T %s: %w", desired, crclient.ObjectKeyFromObject(desired), err)
	}
	return nil
}

func (r *Registry) configure(ctx context.Context, readOnly bool, serving KeyPair) error {
	config := buildConfigMap(r.opts, readOnly)
	if err := r.reconcile(ctx, config); err != nil {
		return fmt.Errorf("configure registry readOnly=%t: %w", readOnly, err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(config.Data["config.yml"])))
	deployment := buildDeployment(r.opts, hash)
	// A retry after a writable-stage failure may have the same config hash,
	// but new TLS material. The registry reads its certificate only at startup.
	deployment.Spec.Template.Annotations[servingHashAnnotation] = fmt.Sprintf("%x", sha256.Sum256(serving.CertPEM))
	if err := r.reconcile(ctx, deployment); err != nil {
		return fmt.Errorf("configure registry readOnly=%t: %w", readOnly, err)
	}
	var lastErr error
	err := wait.PollUntilContextTimeout(ctx, r.pollInterval, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := r.client.Get(ctx, crclient.ObjectKeyFromObject(deployment), deployment); err != nil {
			if apierrors.IsNotFound(err) {
				return false, err
			}
			lastErr = err
			return false, nil
		}
		lastErr = nil
		status := deployment.Status
		return status.ObservedGeneration >= deployment.Generation && status.UpdatedReplicas == 1 && status.AvailableReplicas == 1 && status.Replicas == 1, nil
	})
	if err != nil {
		return fmt.Errorf("wait for readOnly=%t rollout of Deployment %s/%s: %w", readOnly, deployment.Namespace, deployment.Name, errors.Join(err, lastErr))
	}
	return nil
}

func (r *Registry) waitGone(ctx context.Context, obj crclient.Object, cap time.Duration, step string) error {
	key := crclient.ObjectKeyFromObject(obj)
	var lastErr error
	err := wait.PollUntilContextTimeout(ctx, r.pollInterval, cap, true, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		err := r.client.Get(ctx, key, obj)
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		lastErr = err
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("%s: wait for %T %s to be NotFound: %w", step, obj, key, errors.Join(err, lastErr))
	}
	return nil
}

func (r *Registry) waitAdmitted(ctx context.Context, route *routev1.Route) error {
	var lastErr error
	err := wait.PollUntilContextTimeout(ctx, r.pollInterval, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := r.client.Get(ctx, crclient.ObjectKeyFromObject(route), route); err != nil {
			if apierrors.IsNotFound(err) {
				return false, err
			}
			lastErr = err
			return false, nil
		}
		lastErr = nil
		for _, ingress := range route.Status.Ingress {
			for _, condition := range ingress.Conditions {
				if condition.Type == routev1.RouteAdmitted && condition.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("wait for admission of Route %s/%s: %w", route.Namespace, route.Name, errors.Join(err, lastErr))
	}
	return nil
}

func waitEndpoint(ctx context.Context, client *http.Client, host string, exposed bool, pollInterval time.Duration) error {
	cap, step := 2*time.Minute, "wait for routing withdrawal"
	if exposed {
		cap, step = 5*time.Minute, "verify registry endpoint"
	}
	url := "https://" + host + "/v2/"
	var lastErr error
	err := wait.PollUntilContextTimeout(ctx, pollInterval, cap, true, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false, err
		}
		response, err := client.Do(req)
		ok := false
		lastErr = err
		if err == nil {
			ok = response.StatusCode == http.StatusOK
			lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
			response.Body.Close()
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return ok == exposed, nil
	})
	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("%s at %s (last response: %w): %w", step, url, lastErr, err)
		}
		return fmt.Errorf("%s at %s: %w", step, url, err)
	}
	return nil
}

func (r *Registry) teardown(ctx context.Context) error {
	namespace := buildNamespace(r.opts)
	if err := r.client.Delete(ctx, namespace, crclient.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("teardown: delete Namespace %s: %w", namespace.Name, err)
	}
	return r.waitGone(ctx, namespace, 10*time.Minute, "teardown")
}

func buildCopyRequest(opts Options, req MirrorRequest, caPEM []byte) (CopyRequest, error) {
	result := CopyRequest{Namespace: opts.Namespace, CABundlePEM: caPEM, SourcePullSecret: req.SourcePullSecret}
	host := internalHost(opts.Name, opts.Namespace)
	for _, image := range req.Images {
		repo, _, err := splitDigestRef(image.From)
		if err != nil {
			return CopyRequest{}, err
		}
		if image.SourceRepository != "" {
			repo = image.SourceRepository
		}
		mirror, err := mirrorRepository(host, repo)
		if err != nil {
			return CopyRequest{}, err
		}
		result.Images = append(result.Images, ImageCopy{From: image.From, To: mirror})
	}
	for _, release := range req.ReleaseImages {
		repo, _, err := splitDigestRef(release)
		if err != nil {
			return CopyRequest{}, err
		}
		mirror, err := mirrorRepository(host, repo)
		if err != nil {
			return CopyRequest{}, err
		}
		result.Releases = append(result.Releases, ReleaseCopy{From: release, ToRepository: mirror})
	}
	return result, nil
}

func buildResult(opts Options, host string, caPEM []byte, req MirrorRequest, copyRes *CopyResult) (*Result, error) {
	result := &Result{Host: host, InternalHost: internalHost(opts.Name, opts.Namespace), CABundlePEM: caPEM}
	sources := map[string]sets.Set[string]{}
	addSource := func(source, mirror string) {
		if sources[source] == nil {
			sources[source] = sets.New[string]()
		}
		sources[source].Insert(mirror)
	}
	for _, image := range req.Images {
		repo, digest, err := splitDigestRef(image.From)
		if err != nil {
			return nil, err
		}
		if image.SourceRepository != "" {
			repo = image.SourceRepository
		}
		mirror, err := mirrorRepository(host, repo)
		if err != nil {
			return nil, err
		}
		result.Images = append(result.Images, MirroredImage{From: image.From, SourceRef: repo + "@" + digest, MirrorRef: mirror + "@" + digest})
		addSource(repo, mirror)
	}
	for _, release := range req.ReleaseImages {
		repo, _, err := splitDigestRef(release)
		if err != nil {
			return nil, err
		}
		mirror, err := mirrorRepository(host, repo)
		if err != nil {
			return nil, err
		}
		if copyRes == nil {
			return nil, fmt.Errorf("copy result missing requested release %q", release)
		}
		repositories, ok := copyRes.ReleaseSourceRepositories[release]
		if !ok {
			return nil, fmt.Errorf("copy result missing requested release %q", release)
		}
		for _, source := range repositories {
			addSource(source, mirror)
		}
	}
	for source, mirrors := range sources {
		result.ImageContentSources = append(result.ImageContentSources, hyperv1.ImageContentSource{Source: source, Mirrors: sets.List(mirrors)})
	}
	sort.Slice(result.ImageContentSources, func(i, j int) bool {
		return result.ImageContentSources[i].Source < result.ImageContentSources[j].Source
	})
	return result, nil
}
