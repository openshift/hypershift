package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolvePR_GitHubURL(t *testing.T) {
	ref, err := ResolvePR("https://github.com/openshift/hypershift/pull/8761")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Repo != "openshift/hypershift" {
		t.Errorf("repo = %q, want openshift/hypershift", ref.Repo)
	}
	if ref.Number != 8761 {
		t.Errorf("number = %d, want 8761", ref.Number)
	}
}

func TestResolvePR_GitHubURLTrailingSlash(t *testing.T) {
	ref, err := ResolvePR("https://github.com/openshift/hypershift/pull/8761/")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Repo != "openshift/hypershift" {
		t.Errorf("repo = %q, want openshift/hypershift", ref.Repo)
	}
	if ref.Number != 8761 {
		t.Errorf("number = %d, want 8761", ref.Number)
	}
}

func TestResolvePR_OwnerRepoNumber(t *testing.T) {
	ref, err := ResolvePR("openshift/hypershift#8761")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Repo != "openshift/hypershift" {
		t.Errorf("repo = %q, want openshift/hypershift", ref.Repo)
	}
	if ref.Number != 8761 {
		t.Errorf("number = %d, want 8761", ref.Number)
	}
}

func TestResolvePR_HyphenatedOrg(t *testing.T) {
	ref, err := ResolvePR("my-org/my-repo#42")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Repo != "my-org/my-repo" {
		t.Errorf("repo = %q, want my-org/my-repo", ref.Repo)
	}
	if ref.Number != 42 {
		t.Errorf("number = %d, want 42", ref.Number)
	}
}

func TestResolvePR_RejectsInvalid(t *testing.T) {
	_, err := ResolvePR("not-a-pr")
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}

func TestResolvePR_RejectsEmpty(t *testing.T) {
	_, err := ResolvePR("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestGetMergeSHA_Merged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/openshift/hypershift/pulls/8761" {
			t.Errorf("path = %q, want /repos/openshift/hypershift/pulls/8761", r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept = %q, want application/vnd.github+json", r.Header.Get("Accept"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"state":            "closed",
			"merged":           true,
			"merge_commit_sha": "abc123def456",
		})
	}))
	defer srv.Close()

	origBase := githubAPIBase
	defer func() { setGitHubAPIBase(origBase) }()
	setGitHubAPIBase(srv.URL)

	sha, err := GetMergeSHA(srv.Client(), &PRRef{Repo: "openshift/hypershift", Number: 8761})
	if err != nil {
		t.Fatal(err)
	}
	if sha != "abc123def456" {
		t.Errorf("sha = %q, want abc123def456", sha)
	}
}

func TestGetMergeSHA_NotMerged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"state":  "open",
			"merged": false,
		})
	}))
	defer srv.Close()

	origBase := githubAPIBase
	defer func() { setGitHubAPIBase(origBase) }()
	setGitHubAPIBase(srv.URL)

	_, err := GetMergeSHA(srv.Client(), &PRRef{Repo: "openshift/hypershift", Number: 100})
	if err == nil {
		t.Fatal("expected error for unmerged PR")
	}
}

func TestGetMergeSHA_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	origBase := githubAPIBase
	defer func() { setGitHubAPIBase(origBase) }()
	setGitHubAPIBase(srv.URL)

	_, err := GetMergeSHA(srv.Client(), &PRRef{Repo: "openshift/hypershift", Number: 99999})
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestGetMergeSHA_WithToken(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"state":            "closed",
			"merged":           true,
			"merge_commit_sha": "deadbeef",
		})
	}))
	defer srv.Close()

	origBase := githubAPIBase
	defer func() { setGitHubAPIBase(origBase) }()
	setGitHubAPIBase(srv.URL)

	client := newGitHubClient("test-token-123")
	sha, err := GetMergeSHA(client, &PRRef{Repo: "openshift/hypershift", Number: 42})
	if err != nil {
		t.Fatal(err)
	}
	if sha != "deadbeef" {
		t.Errorf("sha = %q, want deadbeef", sha)
	}
	if receivedAuth != "Bearer test-token-123" {
		t.Errorf("Authorization = %q, want Bearer test-token-123", receivedAuth)
	}
}
