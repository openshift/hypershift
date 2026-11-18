package machine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/client/clientset/clientset/scheme"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func TestSetup(t *testing.T) {
	tests := []struct {
		name               string
		platformType       hyperv1.PlatformType
		forbidServiceList  bool
		forbidServiceWatch bool
		wantError          string
		wantCacheAdded     bool
	}{
		{
			name:         "When the platform is not KubeVirt, it should skip KubeVirt setup",
			platformType: hyperv1.AWSPlatform,
		},
		{
			name:              "When the infra API forbids listing services, it should return an actionable error",
			platformType:      hyperv1.KubevirtPlatform,
			forbidServiceList: true,
			wantError:         `failed to list services in kubevirt infra namespace "infra-ns": services is forbidden`,
		},
		{
			name:               "When the infra API forbids watching services, it should return an actionable error",
			platformType:       hyperv1.KubevirtPlatform,
			forbidServiceWatch: true,
			wantError:          `failed to watch services in kubevirt infra namespace "infra-ns": services is forbidden`,
		},
		{
			name:           "When the infra API allows listing services, it should register the cache and sync check",
			platformType:   hyperv1.KubevirtPlatform,
			wantCacheAdded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := NewWithT(t)
			mgr := &recordingManager{}

			opts := &operator.HostedClusterConfigOperatorConfig{
				Manager:      mgr,
				CPCluster:    setupCPCluster{},
				HCPName:      "hcp",
				Namespace:    "ns",
				PlatformType: tt.platformType,
			}
			if tt.platformType == hyperv1.KubevirtPlatform {
				server := newKubevirtInfraAPIServer(t, tt.forbidServiceList, tt.forbidServiceWatch)
				defer server.Close()
				opts.KubevirtInfraConfig = &rest.Config{Host: server.URL}
			}

			err := Setup(t.Context(), opts)
			if tt.wantError != "" {
				gt.Expect(err).To(MatchError(ContainSubstring(tt.wantError)))
				gt.Expect(mgr.added).To(BeEmpty())
				return
			}
			gt.Expect(err).NotTo(HaveOccurred())

			var cacheAdded bool
			var syncChecks int
			for _, runnable := range mgr.added {
				if _, ok := runnable.(cache.Cache); ok {
					cacheAdded = true
				}
				if _, ok := runnable.(manager.RunnableFunc); ok {
					syncChecks++
				}
			}
			gt.Expect(cacheAdded).To(Equal(tt.wantCacheAdded))
			if tt.wantCacheAdded {
				gt.Expect(syncChecks).To(Equal(1))
			}
		})
	}
}

func TestValidateKubevirtInfraAccess(t *testing.T) {
	tests := []struct {
		name     string
		listErr  error
		watchErr error
		wantErr  string
	}{
		{
			name: "When the infra API allows listing and watching services, it should return no error",
		},
		{
			name:    "When the infra API forbids listing services, it should return a contextual error",
			listErr: apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "", errors.New("denied")),
			wantErr: `failed to list services in kubevirt infra namespace "infra-ns": services is forbidden`,
		},
		{
			name:     "When the infra API forbids watching services, it should return a contextual error",
			watchErr: apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "", errors.New("denied")),
			wantErr:  `failed to watch services in kubevirt infra namespace "infra-ns": services is forbidden`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := NewWithT(t)
			infraScheme := runtime.NewScheme()
			gt.Expect(corev1.AddToScheme(infraScheme)).To(Succeed())
			funcs := interceptor.Funcs{}
			if tt.listErr != nil {
				funcs.List = func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
					return tt.listErr
				}
			}
			if tt.watchErr != nil {
				funcs.Watch = func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) (watch.Interface, error) {
					return nil, tt.watchErr
				}
			}

			infraClient := fake.NewClientBuilder().WithScheme(infraScheme).WithInterceptorFuncs(funcs).Build()

			err := validateKubevirtInfraAccess(t.Context(), infraClient, "infra-ns")
			if tt.wantErr != "" {
				gt.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)))
				return
			}
			gt.Expect(err).NotTo(HaveOccurred())
		})
	}
}

func TestWaitForKubevirtInfraCacheSync(t *testing.T) {
	tests := []struct {
		name         string
		cacheContext func(context.Context) context.Context
		waitForSync  func(context.Context) bool
		timeout      time.Duration
		wantErr      string
	}{
		{
			name:        "When the infra cache syncs, it should return no error",
			waitForSync: func(context.Context) bool { return true },
			timeout:     time.Second,
		},
		{
			name: "When the infra cache does not sync before the timeout, it should return an error",
			waitForSync: func(ctx context.Context) bool {
				<-ctx.Done()
				return false
			},
			timeout: 10 * time.Millisecond,
			wantErr: "timed out after 10ms waiting for kubevirt infra cache to sync",
		},
		{
			name: "When the manager is shutting down, it should return no error",
			cacheContext: func(ctx context.Context) context.Context {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				return ctx
			},
			waitForSync: func(ctx context.Context) bool {
				<-ctx.Done()
				return false
			},
			timeout: time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := NewWithT(t)
			ctx := t.Context()
			if tt.cacheContext != nil {
				ctx = tt.cacheContext(ctx)
			}

			err := waitForKubevirtInfraCacheSync(ctx, setupCache{waitForSync: tt.waitForSync}, tt.timeout)
			if tt.wantErr != "" {
				gt.Expect(err).To(MatchError(ContainSubstring(tt.wantErr)))
				return
			}
			gt.Expect(err).NotTo(HaveOccurred())
		})
	}

	t.Run("When a real infra cache cannot list services, it should time out waiting for sync", func(t *testing.T) {
		gt := NewWithT(t)
		server := newKubevirtInfraAPIServer(t, true, false)
		defer server.Close()

		infraConfig := &rest.Config{Host: server.URL}
		httpClient, err := rest.HTTPClientFor(infraConfig)
		gt.Expect(err).NotTo(HaveOccurred())
		mapper, err := apiutil.NewDynamicRESTMapper(infraConfig, httpClient)
		gt.Expect(err).NotTo(HaveOccurred())
		infraScheme := runtime.NewScheme()
		gt.Expect(corev1.AddToScheme(infraScheme)).To(Succeed())

		infraCache, err := cache.New(infraConfig, cache.Options{
			Scheme: infraScheme,
			Mapper: mapper,
			DefaultNamespaces: map[string]cache.Config{
				"infra-ns": {},
			},
		})
		gt.Expect(err).NotTo(HaveOccurred())
		_, err = infraCache.GetInformer(t.Context(), &corev1.Service{}, cache.BlockUntilSynced(false))
		gt.Expect(err).NotTo(HaveOccurred())

		cacheCtx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = infraCache.Start(cacheCtx)
		}()
		defer func() {
			cancel()
			<-done
		}()

		err = waitForKubevirtInfraCacheSync(cacheCtx, infraCache, 100*time.Millisecond)
		gt.Expect(err).To(MatchError(ContainSubstring("timed out after 100ms waiting for kubevirt infra cache to sync")))
	})
}

type recordingManager struct {
	manager.Manager
	added []manager.Runnable
}

func (m *recordingManager) Add(runnable manager.Runnable) error {
	m.added = append(m.added, runnable)
	return nil
}

func (recordingManager) GetControllerOptions() ctrlconfig.Controller {
	return ctrlconfig.Controller{SkipNameValidation: ptr.To(true)}
}

type setupCPCluster struct {
	cluster.Cluster
}

func (setupCPCluster) GetAPIReader() client.Reader {
	return fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(&hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns",
				Name:      "hcp",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Kubevirt: &hyperv1.KubevirtPlatformSpec{
						Credentials: &hyperv1.KubevirtPlatformCredentials{InfraNamespace: "infra-ns"},
					},
				},
			},
		}).
		Build()
}

func (setupCPCluster) GetClient() client.Client {
	return nil
}

func (setupCPCluster) GetCache() cache.Cache {
	return nil
}

type setupCache struct {
	cache.Cache
	waitForSync func(context.Context) bool
}

func (c setupCache) WaitForCacheSync(ctx context.Context) bool {
	return c.waitForSync(ctx)
}

func newKubevirtInfraAPIServer(t *testing.T, forbidServiceList, forbidServiceWatch bool) *httptest.Server {
	t.Helper()
	writeResponse := func(w http.ResponseWriter, format string, args ...any) {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			t.Errorf("failed to write test API response: %v", err)
		}
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			writeResponse(w, `{"kind":"APIVersions","apiVersion":"v1","versions":["v1"],"serverAddressByClientCIDRs":[]}`)
		case "/apis":
			writeResponse(w, `{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`)
		case "/api/v1":
			writeResponse(w, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[{"name":"services","singularName":"service","namespaced":true,"kind":"Service","verbs":["get","list","watch"]}]}`)
		case "/api/v1/namespaces/infra-ns/services":
			if r.URL.Query().Get("watch") == "true" {
				if forbidServiceWatch {
					w.WriteHeader(http.StatusForbidden)
					writeResponse(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"services is forbidden","reason":"Forbidden","code":403}`)
					return
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			if forbidServiceList {
				w.WriteHeader(http.StatusForbidden)
				writeResponse(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"services is forbidden","reason":"Forbidden","code":403}`)
				return
			}
			writeResponse(w, `{"kind":"ServiceList","apiVersion":"v1","metadata":{},"items":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			writeResponse(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"not found: %s","reason":"NotFound","code":404}`, r.URL.Path)
		}
	}))
}
