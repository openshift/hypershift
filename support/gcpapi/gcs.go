package gcpapi

import (
	"context"
	"io"
)

//go:generate ../../hack/tools/bin/mockgen -source=gcs.go -package=gcpapi -destination=gcs_mock.go

// GCSAPI defines the GCS operations used by the hosted cluster controller for OIDC document management.
type GCSAPI interface {
	UploadObject(ctx context.Context, bucket, objectName string, content io.Reader) error
	DeleteObject(ctx context.Context, bucket, objectName string) error
}
