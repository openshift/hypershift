package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Querier abstracts all Kubernetes/KubeArchive API calls.
type Querier interface {
	ListPipelineRuns(sha string) ([]PipelineRun, error)
	ListReleases(sha string) ([]Release, error)
	ListReleasePipelineRuns(releaseName string) ([]PipelineRun, error)
	GetReleasePlan(name string) (*ReleasePlan, error)
	GetReleasePlanAdmission(namespace, name string) (*ReleasePlanAdmission, error)
	GetSnapshot(name string) (*Snapshot, error)
}

// AppConfig holds runtime configuration.
type AppConfig struct {
	KonfluxNamespace string
	RelengNamespace  string
	KAHost           string
}

type httpQuerier struct {
	client           *http.Client
	kubeHost         string
	kaHost           string
	konfluxNamespace string
	relengNamespace  string
	stderr           io.Writer
	archiveNotified  map[string]bool
}

func newHTTPQuerier(cfg AppConfig, stderr io.Writer) (*httpQuerier, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{})

	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	httpClient, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("building HTTP client: %w", err)
	}
	if httpClient.Timeout == 0 {
		httpClient.Timeout = 30 * time.Second
	}

	return &httpQuerier{
		client:           httpClient,
		kubeHost:         restConfig.Host,
		kaHost:           cfg.KAHost,
		konfluxNamespace: cfg.KonfluxNamespace,
		relengNamespace:  cfg.RelengNamespace,
		stderr:           stderr,
		archiveNotified:  make(map[string]bool),
	}, nil
}

func (q *httpQuerier) ListPipelineRuns(sha string) ([]PipelineRun, error) {
	sel := "pipelinesascode.tekton.dev/sha=" + sha + ",pipelinesascode.tekton.dev/event-type=push"
	path := "/apis/tekton.dev/v1/namespaces/" + q.konfluxNamespace + "/pipelineruns"
	return listWithFallback[PipelineRun](q, path, sel, "PipelineRuns")
}

func (q *httpQuerier) ListReleases(sha string) ([]Release, error) {
	sel := "pac.test.appstudio.openshift.io/sha=" + sha
	path := "/apis/appstudio.redhat.com/v1alpha1/namespaces/" + q.konfluxNamespace + "/releases"
	return listWithFallback[Release](q, path, sel, "Releases")
}

func (q *httpQuerier) ListReleasePipelineRuns(releaseName string) ([]PipelineRun, error) {
	sel := "release.appstudio.openshift.io/name=" + releaseName
	path := "/apis/tekton.dev/v1/namespaces/" + q.relengNamespace + "/pipelineruns"
	return listWithFallback[PipelineRun](q, path, sel, "release PipelineRuns")
}

func (q *httpQuerier) GetReleasePlan(name string) (*ReleasePlan, error) {
	path := "/apis/appstudio.redhat.com/v1alpha1/namespaces/" + q.konfluxNamespace + "/releaseplans/" + name
	return getResource[ReleasePlan](q, q.kubeHost, path)
}

func (q *httpQuerier) GetReleasePlanAdmission(namespace, name string) (*ReleasePlanAdmission, error) {
	path := "/apis/appstudio.redhat.com/v1alpha1/namespaces/" + namespace + "/releaseplanadmissions/" + name
	return getResource[ReleasePlanAdmission](q, q.kubeHost, path)
}

func (q *httpQuerier) GetSnapshot(name string) (*Snapshot, error) {
	path := "/apis/appstudio.redhat.com/v1alpha1/namespaces/" + q.konfluxNamespace + "/snapshots/" + name
	return getResource[Snapshot](q, q.kubeHost, path)
}

func listWithFallback[T any](q *httpQuerier, apiPath, labelSelector, resourceName string) ([]T, error) {
	items, err := listResource[T](q, q.kubeHost, apiPath, labelSelector)
	if err == nil && len(items) > 0 {
		return items, nil
	}

	if q.kaHost == "" {
		if err != nil {
			return nil, err
		}
		return items, nil
	}

	if !q.archiveNotified[resourceName] {
		fmt.Fprintf(q.stderr, "No live %s found, querying KubeArchive...\n", resourceName)
	}
	items, err = listResource[T](q, q.kaHost, apiPath, labelSelector)
	if err != nil {
		return nil, fmt.Errorf("KubeArchive query for %s failed: %w", resourceName, err)
	}
	if !q.archiveNotified[resourceName] && len(items) > 0 {
		fmt.Fprintf(q.stderr, "(source: KubeArchive)\n\n")
	}
	q.archiveNotified[resourceName] = true
	return items, nil
}

func listResource[T any](q *httpQuerier, host, path, labelSelector string) ([]T, error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("parsing host URL %q: %w", host, err)
	}
	u.Path = path
	params := u.Query()
	params.Set("labelSelector", labelSelector)
	u.RawQuery = params.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil) //nolint:noctx
	if err != nil {
		return nil, err
	}

	resp, err := q.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, u.String(), string(body))
	}

	var list resourceList[T]
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return list.Items, nil
}

func getResource[T any](q *httpQuerier, host, path string) (*T, error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("parsing host URL %q: %w", host, err)
	}
	u.Path = path

	req, err := http.NewRequest(http.MethodGet, u.String(), nil) //nolint:noctx
	if err != nil {
		return nil, err
	}

	resp, err := q.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, u.String(), string(body))
	}

	var resource T
	if err := json.NewDecoder(resp.Body).Decode(&resource); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &resource, nil
}
