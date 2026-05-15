package etcdupload

import (
	"context"
	"fmt"
	"io"
	"testing"

	. "github.com/onsi/gomega"
)

type mockGCSClient struct {
	bucket    string
	object    string
	uploadErr error
	uploaded  []byte
}

func (c *mockGCSClient) Upload(ctx context.Context, bucket, object string, reader io.Reader) error {
	c.bucket = bucket
	c.object = object
	if c.uploadErr != nil {
		return c.uploadErr
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	c.uploaded = data
	return nil
}

func newTestGCSUploader(bucket string, client GCSWriterAPI) *GCSUploader {
	return &GCSUploader{
		bucket: bucket,
		client: client,
	}
}

func TestGCSUploader(t *testing.T) {
	t.Run("When uploading successfully it should return the correct GCS URL", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockGCSClient{}
		uploader := newTestGCSUploader("my-bucket", mock)
		snapshotPath := createTempSnapshot(t)

		result, err := uploader.Upload(context.Background(), snapshotPath, "my-infra/12345.db")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result.URL).To(Equal("gs://my-bucket/my-infra/12345.db"))
		g.Expect(mock.bucket).To(Equal("my-bucket"))
		g.Expect(mock.object).To(Equal("my-infra/12345.db"))
	})

	t.Run("When upload fails it should return the error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockGCSClient{uploadErr: fmt.Errorf("upload failed: network error")}
		uploader := newTestGCSUploader("my-bucket", mock)
		snapshotPath := createTempSnapshot(t)

		_, err := uploader.Upload(context.Background(), snapshotPath, "my-infra/12345.db")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("network error"))
	})

	t.Run("When snapshot file does not exist it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockGCSClient{}
		uploader := newTestGCSUploader("my-bucket", mock)

		_, err := uploader.Upload(context.Background(), "/nonexistent/snapshot.db", "my-infra/12345.db")
		g.Expect(err).To(HaveOccurred())
	})
}
