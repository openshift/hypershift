package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListPipelineRuns_LiveSuccess(t *testing.T) {
	var receivedSelector string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSelector = r.URL.Query().Get("labelSelector")
		_ = json.NewEncoder(w).Encode(resourceList[PipelineRun]{
			Items: []PipelineRun{
				{
					Metadata: ObjectMeta{Name: "plr-1", CreationTimestamp: "2026-07-06T10:00:00Z"},
					Status:   PipelineRunStatus{Conditions: []Condition{{Type: "Succeeded", Reason: "Completed"}}},
				},
			},
		})
	}))
	defer srv.Close()

	q := &httpQuerier{
		client:           srv.Client(),
		kubeHost:         srv.URL,
		konfluxNamespace: "test-ns",
		relengNamespace:  "releng-ns",
		stderr:           io.Discard,
		archiveNotified:  make(map[string]bool),
	}

	prs, err := q.ListPipelineRuns("deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 PipelineRun, got %d", len(prs))
	}
	if prs[0].Metadata.Name != "plr-1" {
		t.Errorf("name = %q, want plr-1", prs[0].Metadata.Name)
	}
	if !strings.Contains(receivedSelector, "pipelinesascode.tekton.dev/sha=deadbeef") {
		t.Errorf("selector missing sha: %q", receivedSelector)
	}
	if !strings.Contains(receivedSelector, "pipelinesascode.tekton.dev/event-type=push") {
		t.Errorf("selector missing event-type: %q", receivedSelector)
	}
}

func TestListPipelineRuns_FallbackToArchive(t *testing.T) {
	liveHits := 0
	liveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		liveHits++
		_ = json.NewEncoder(w).Encode(resourceList[PipelineRun]{Items: nil})
	}))
	defer liveSrv.Close()

	archiveHits := 0
	archiveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		archiveHits++
		_ = json.NewEncoder(w).Encode(resourceList[PipelineRun]{
			Items: []PipelineRun{
				{Metadata: ObjectMeta{Name: "archived-plr", CreationTimestamp: "2026-07-06T09:00:00Z"}},
			},
		})
	}))
	defer archiveSrv.Close()

	q := &httpQuerier{
		client:           liveSrv.Client(),
		kubeHost:         liveSrv.URL,
		kaHost:           archiveSrv.URL,
		konfluxNamespace: "test-ns",
		relengNamespace:  "releng-ns",
		stderr:           io.Discard,
		archiveNotified:  make(map[string]bool),
	}

	prs, err := q.ListPipelineRuns("deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if liveHits != 1 {
		t.Errorf("expected 1 live hit, got %d", liveHits)
	}
	if archiveHits != 1 {
		t.Errorf("expected 1 archive hit, got %d", archiveHits)
	}
	if len(prs) != 1 || prs[0].Metadata.Name != "archived-plr" {
		t.Errorf("expected archived-plr, got %v", prs)
	}
}

func TestListReleases_CorrectSelector(t *testing.T) {
	var receivedSelector string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSelector = r.URL.Query().Get("labelSelector")
		_ = json.NewEncoder(w).Encode(resourceList[Release]{
			Items: []Release{
				{Metadata: ObjectMeta{Name: "rel-1", CreationTimestamp: "2026-07-06T10:00:00Z"}},
			},
		})
	}))
	defer srv.Close()

	q := &httpQuerier{
		client:           srv.Client(),
		kubeHost:         srv.URL,
		konfluxNamespace: "test-ns",
		relengNamespace:  "releng-ns",
		stderr:           io.Discard,
		archiveNotified:  make(map[string]bool),
	}

	_, err := q.ListReleases("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if receivedSelector != "pac.test.appstudio.openshift.io/sha=abc123" {
		t.Errorf("selector = %q, want pac.test.appstudio.openshift.io/sha=abc123", receivedSelector)
	}
}

func TestListReleasePipelineRuns_CorrectNamespace(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(resourceList[PipelineRun]{Items: nil})
	}))
	defer srv.Close()

	q := &httpQuerier{
		client:           srv.Client(),
		kubeHost:         srv.URL,
		konfluxNamespace: "tenant-ns",
		relengNamespace:  "releng-ns",
		stderr:           io.Discard,
		archiveNotified:  make(map[string]bool),
	}

	_, err := q.ListReleasePipelineRuns("rel-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receivedPath, "/releng-ns/") {
		t.Errorf("expected releng namespace in path, got %q", receivedPath)
	}
}

func TestGetReleasePlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rp := ReleasePlan{}
		rp.Spec.Data.Mapping = &Mapping{
			Components: []MappingComponent{
				{Name: "comp-1", Repositories: []MappingRepository{{URL: "quay.io/dest"}}},
			},
		}
		_ = json.NewEncoder(w).Encode(rp)
	}))
	defer srv.Close()

	q := &httpQuerier{
		client:           srv.Client(),
		kubeHost:         srv.URL,
		konfluxNamespace: "test-ns",
		relengNamespace:  "releng-ns",
		stderr:           io.Discard,
		archiveNotified:  make(map[string]bool),
	}

	rp, err := q.GetReleasePlan("my-plan")
	if err != nil {
		t.Fatal(err)
	}
	if rp.Spec.Data.Mapping == nil {
		t.Fatal("expected mapping")
	}
	if len(rp.Spec.Data.Mapping.Components) != 1 {
		t.Fatalf("expected 1 component, got %d", len(rp.Spec.Data.Mapping.Components))
	}
}
