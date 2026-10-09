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
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	backupArchiveAnnotation = "hypershift.openshift.io/restored-from-backup"
	maxArchiveItemSize      = 20 << 20
)

// DownloadBackupContents asks Velero for a signed URL and downloads the backup
// archive. The caller must close the returned reader.
func DownloadBackupContents(ctx context.Context, client crclient.Client, namespace, backupName string) (io.ReadCloser, error) {
	request := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "velero.io/v1",
		"kind":       "DownloadRequest",
		"metadata": map[string]interface{}{
			"generateName": backupName + "-contents-",
			"namespace":    namespace,
		},
		"spec": map[string]interface{}{
			"target": map[string]interface{}{
				"kind": "BackupContents",
				"name": backupName,
			},
		},
	}}
	if err := client.Create(ctx, request); err != nil {
		return nil, fmt.Errorf("create DownloadRequest for backup %s: %w", backupName, err)
	}
	defer func() { _ = client.Delete(ctx, request) }()

	var downloadURL string
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		current := &unstructured.Unstructured{}
		current.SetGroupVersionKind(schema.GroupVersionKind{Group: "velero.io", Version: "v1", Kind: "DownloadRequest"})
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(request), current); err != nil {
			return false, err
		}
		phase, _, err := unstructured.NestedString(current.Object, "status", "phase")
		if err != nil {
			return false, err
		}
		if phase == "Failed" {
			return false, fmt.Errorf("DownloadRequest %s failed", request.GetName())
		}
		downloadURL, _, err = unstructured.NestedString(current.Object, "status", "downloadURL")
		return phase == "Processed" && downloadURL != "", err
	})
	if err != nil {
		return nil, fmt.Errorf("wait for DownloadRequest %s: %w", request.GetName(), err)
	}

	httpClient := &http.Client{Timeout: 2 * time.Minute}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create backup archive request: %w", err)
	}
	response, err := httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("download backup %s: %w", backupName, err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("download backup %s: HTTP %d", backupName, response.StatusCode)
	}
	return response.Body, nil
}

// BackupArchiveHasHostedClusterRestoreAnnotation checks the copy of a
// HostedCluster in a Velero backup archive. The annotation is written by the
// HyperShift backup item action, so it proves the plugin processed the item.
func BackupArchiveHasHostedClusterRestoreAnnotation(archive io.Reader, namespace, name string) (bool, error) {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return false, fmt.Errorf("open backup archive: %w", err)
	}
	defer gz.Close()

	tarReader := tar.NewReader(gz)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read backup archive: %w", err)
		}
		if (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) || !strings.HasSuffix(header.Name, ".json") {
			continue
		}
		if header.Size > maxArchiveItemSize {
			return false, fmt.Errorf("backup archive item %s exceeds %d bytes", header.Name, maxArchiveItemSize)
		}
		itemData, err := io.ReadAll(io.LimitReader(tarReader, maxArchiveItemSize+1))
		if err != nil {
			return false, fmt.Errorf("read backup archive item %s: %w", header.Name, err)
		}
		var item map[string]interface{}
		if err := json.Unmarshal(itemData, &item); err != nil {
			continue // The archive also contains metadata JSON files.
		}
		kind, _, _ := unstructured.NestedString(item, "kind")
		itemNamespace, _, _ := unstructured.NestedString(item, "metadata", "namespace")
		itemName, _, _ := unstructured.NestedString(item, "metadata", "name")
		if kind != "HostedCluster" || itemNamespace != namespace || itemName != name {
			continue
		}
		_, found, err := unstructured.NestedString(item, "metadata", "annotations", backupArchiveAnnotation)
		return found, err
	}
}
