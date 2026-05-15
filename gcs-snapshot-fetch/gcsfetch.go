package gcsfetch

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	gcsapi "google.golang.org/api/storage/v1"
)

type options struct {
	gcsBucket string
	infraID   string
	outputDir string
}

func NewStartCommand() *cobra.Command {
	opts := options{}

	cmd := &cobra.Command{
		Use:          "gcs-snapshot-fetch",
		Short:        "Download the latest etcd snapshot from GCS for a given infraID",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			return run(ctx, opts)
		},
	}

	cmd.Flags().StringVar(&opts.gcsBucket, "gcs-bucket", "", "GCS bucket name")
	cmd.Flags().StringVar(&opts.infraID, "infra-id", "", "infrastructure ID to look up snapshots for")
	cmd.Flags().StringVar(&opts.outputDir, "output-dir", "/snapshot", "directory to write the downloaded snapshot to")

	_ = cmd.MarkFlagRequired("gcs-bucket")
	_ = cmd.MarkFlagRequired("infra-id")

	return cmd
}

func run(ctx context.Context, opts options) error {
	service, err := gcsapi.NewService(ctx)
	if err != nil {
		return fmt.Errorf("failed to create GCS client: %w", err)
	}

	lister := &gcsLister{service: service}
	return fetchLatestSnapshotWith(ctx, lister, opts)
}

// GCSObjectLister abstracts GCS object listing for testability.
type GCSObjectLister interface {
	ListObjects(ctx context.Context, bucket, prefix string) ([]string, error)
	DownloadObject(ctx context.Context, bucket, object, destPath string) error
}

type gcsLister struct {
	service *gcsapi.Service
}

func (l *gcsLister) ListObjects(ctx context.Context, bucket, prefix string) ([]string, error) {
	var names []string
	pageToken := ""
	for {
		call := l.service.Objects.List(bucket).Prefix(prefix).Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		result, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("failed to list objects in gs://%s/%s: %w", bucket, prefix, err)
		}
		for _, item := range result.Items {
			names = append(names, item.Name)
		}
		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}
	return names, nil
}

func (l *gcsLister) DownloadObject(ctx context.Context, bucket, object, destPath string) error {
	resp, err := l.service.Objects.Get(bucket, object).Context(ctx).Download()
	if err != nil {
		return fmt.Errorf("failed to open gs://%s/%s: %w", bucket, object, err)
	}
	defer resp.Body.Close()

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", destPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("failed to download gs://%s/%s: %w", bucket, object, err)
	}
	return nil
}

func fetchLatestSnapshotWith(ctx context.Context, lister GCSObjectLister, opts options) error {
	prefix := opts.infraID + "/"

	names, err := lister.ListObjects(ctx, opts.gcsBucket, prefix)
	if err != nil {
		return err
	}

	if len(names) == 0 {
		msg := "no-snapshot"
		fmt.Println(msg)
		writeTerminationMessage(msg)
		return nil
	}

	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	latest := names[0]

	if err := os.MkdirAll(opts.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	fmt.Printf("Downloading latest snapshot: %s\n", latest)

	if strings.HasSuffix(latest, ".tar.gz") {
		tmpFile, err := os.CreateTemp("", "gcs-snapshot-*.tar.gz")
		if err != nil {
			return fmt.Errorf("failed to create temp file: %w", err)
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		if err := lister.DownloadObject(ctx, opts.gcsBucket, latest, tmpPath); err != nil {
			return err
		}
		if err := extractArchive(tmpPath, opts.outputDir); err != nil {
			return fmt.Errorf("failed to extract archive: %w", err)
		}
	} else {
		destPath := filepath.Join(opts.outputDir, "snapshot.db")
		if err := lister.DownloadObject(ctx, opts.gcsBucket, latest, destPath); err != nil {
			return err
		}
	}

	msg := fmt.Sprintf("snapshot-ready:%s", latest)
	fmt.Println(msg)
	writeTerminationMessage(msg)
	return nil
}

func extractArchive(archivePath, outputDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	foundSnapshot := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		clean := filepath.Clean(hdr.Name)
		if strings.Contains(clean, "..") {
			continue
		}

		destPath := filepath.Join(outputDir, clean)
		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", destPath, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %w", destPath, err)
		}

		outFile, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("failed to create %s: %w", destPath, err)
		}
		if _, err := io.Copy(outFile, tr); err != nil {
			outFile.Close()
			return fmt.Errorf("failed to write %s: %w", destPath, err)
		}
		outFile.Close()

		if clean == "snapshot.db" {
			foundSnapshot = true
		}
	}

	if !foundSnapshot {
		return fmt.Errorf("archive does not contain snapshot.db")
	}
	return nil
}

func writeTerminationMessage(msg string) {
	if err := os.WriteFile("/dev/termination-log", []byte(msg), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write termination log: %v\n", err)
	}
}
