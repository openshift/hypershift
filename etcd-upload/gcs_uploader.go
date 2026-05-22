package etcdupload

import (
	"context"
	"fmt"
	"io"
	"os"

	gcsapi "google.golang.org/api/storage/v1"
)

// GCSUploader uploads etcd snapshots to Google Cloud Storage.
type GCSUploader struct {
	bucket string
	client GCSWriterAPI
}

// GCSWriterAPI defines the GCS client interface used by the uploader.
type GCSWriterAPI interface {
	Upload(ctx context.Context, bucket, object string, reader io.Reader) error
}

type gcsClientWrapper struct {
	service *gcsapi.Service
}

func (w *gcsClientWrapper) Upload(ctx context.Context, bucket, object string, reader io.Reader) error {
	obj := &gcsapi.Object{Name: object}
	call := w.service.Objects.Insert(bucket, obj).Media(reader).Context(ctx)
	_, err := call.Do()
	return err
}

// NewGCSUploader creates a new GCSUploader.
// Authentication uses Application Default Credentials (ADC), which is
// automatically provided by GKE Workload Identity — no credentials file needed.
func NewGCSUploader(ctx context.Context, bucket string) (*GCSUploader, error) {
	if bucket == "" {
		return nil, fmt.Errorf("--gcs-bucket is required for GCS storage type")
	}

	service, err := gcsapi.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &GCSUploader{
		bucket: bucket,
		client: &gcsClientWrapper{service: service},
	}, nil
}

// Upload uploads a snapshot file to GCS.
func (u *GCSUploader) Upload(ctx context.Context, snapshotPath string, key string) (*UploadResult, error) {
	f, err := os.Open(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open snapshot file %q: %w", snapshotPath, err)
	}
	defer f.Close()

	if err := u.client.Upload(ctx, u.bucket, key, f); err != nil {
		return nil, fmt.Errorf("failed to upload to gs://%s/%s: %w", u.bucket, key, err)
	}

	return &UploadResult{
		URL: fmt.Sprintf("gs://%s/%s", u.bucket, key),
	}, nil
}
