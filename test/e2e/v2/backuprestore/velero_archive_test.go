//go:build e2ev2 && backuprestore

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package backuprestore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type downloadRequestClient struct {
	crclient.Client
	downloadURL string
	created     bool
	deleted     bool
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (c *downloadRequestClient) Create(_ context.Context, obj crclient.Object, _ ...crclient.CreateOption) error {
	obj.SetName("backup-contents-test")
	c.created = true
	return nil
}

func (c *downloadRequestClient) Get(_ context.Context, _ crclient.ObjectKey, obj crclient.Object, _ ...crclient.GetOption) error {
	request := obj.(*unstructured.Unstructured)
	request.Object["status"] = map[string]interface{}{
		"phase": "Processed", "downloadURL": c.downloadURL,
	}
	return nil
}

func (c *downloadRequestClient) Delete(_ context.Context, _ crclient.Object, _ ...crclient.DeleteOption) error {
	c.deleted = true
	return nil
}

func TestDownloadBackupContents(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{name: "When Velero provides an archive URL, it should download the archive and delete the request", statusCode: http.StatusOK},
		{name: "When archive download fails, it should report the HTTP status and delete the request", statusCode: http.StatusInternalServerError, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousTransport := http.DefaultTransport
			http.DefaultTransport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.String() != "https://example.invalid/archive" {
					t.Errorf("unexpected archive request: %s %s", request.Method, request.URL)
				}
				return &http.Response{
					StatusCode: tc.statusCode,
					Body:       io.NopCloser(strings.NewReader("archive")),
					Header:     make(http.Header),
				}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = previousTransport })

			client := &downloadRequestClient{downloadURL: "https://example.invalid/archive"}
			archive, err := DownloadBackupContents(context.Background(), client, "openshift-adp", "backup")
			if !client.created || !client.deleted {
				t.Fatalf("DownloadRequest lifecycle: created=%v, deleted=%v", client.created, client.deleted)
			}
			if tc.wantError {
				if err == nil || archive != nil {
					t.Fatalf("expected HTTP download error, got archive=%v, err=%v", archive, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			data, err := io.ReadAll(archive)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "archive" {
				t.Fatalf("got archive %q, want %q", data, "archive")
			}
		})
	}
}

func TestBackupArchiveHasHostedClusterRestoreAnnotation(t *testing.T) {
	archive := func(withAnnotation bool) *bytes.Buffer {
		t.Helper()
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		item := `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"example","namespace":"clusters"}}`
		if withAnnotation {
			item = `{"apiVersion":"hypershift.openshift.io/v1beta1","kind":"HostedCluster","metadata":{"name":"example","namespace":"clusters","annotations":{"hypershift.openshift.io/restored-from-backup":""}}}`
		}
		if err := tw.WriteHeader(&tar.Header{Name: "resources/hostedclusters/example.json", Mode: 0600, Size: int64(len(item))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(item)); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		return &buf
	}

	for _, tc := range []struct {
		name           string
		withAnnotation bool
		namespace      string
		want           bool
	}{
		{name: "When the plugin annotated the HostedCluster, it should find the annotation", withAnnotation: true, namespace: "clusters", want: true},
		{name: "When the plugin did not annotate the HostedCluster, it should report no annotation", namespace: "clusters"},
		{name: "When the archive contains a different HostedCluster, it should report no annotation", withAnnotation: true, namespace: "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, err := BackupArchiveHasHostedClusterRestoreAnnotation(archive(tc.withAnnotation), tc.namespace, "example")
			if err != nil {
				t.Fatal(err)
			}
			if found != tc.want {
				t.Fatalf("found=%v, want %v", found, tc.want)
			}
		})
	}
}
