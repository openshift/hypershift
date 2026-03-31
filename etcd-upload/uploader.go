package etcdupload

import "context"

// UploadResult contains the result of an upload operation.
type UploadResult struct {
	// URL is the cloud storage URL of the uploaded snapshot.
	URL string
}

// Uploader uploads an etcd snapshot to cloud storage.
type Uploader interface {
	Upload(ctx context.Context, snapshotPath string, key string) (*UploadResult, error)
}
