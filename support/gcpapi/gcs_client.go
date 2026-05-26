package gcpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	storage "google.golang.org/api/storage/v1"
)

// GCSClient wraps the auto-generated GCS REST client to implement GCSAPI.
type GCSClient struct {
	objects *storage.ObjectsService
}

// Ensure GCSClient implements GCSAPI at compile time.
var _ GCSAPI = (*GCSClient)(nil)

func NewGCSClient(ctx context.Context, opts ...option.ClientOption) (*GCSClient, error) {
	svc, err := storage.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS storage service: %w", err)
	}
	return &GCSClient{objects: svc.Objects}, nil
}

func (g *GCSClient) UploadObject(ctx context.Context, bucket, objectName string, content io.Reader) error {
	obj := &storage.Object{
		Name:        objectName,
		ContentType: "application/json",
	}
	_, err := g.objects.Insert(bucket, obj).Media(content).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to upload to gs://%s/%s: %w", bucket, objectName, err)
	}
	return nil
}

func (g *GCSClient) DeleteObject(ctx context.Context, bucket, objectName string) error {
	err := g.objects.Delete(bucket, objectName).Context(ctx).Do()
	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("failed to delete gs://%s/%s: %w", bucket, objectName, err)
	}
	return nil
}
