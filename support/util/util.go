package util

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DebugDeploymentsAnnotation contains a comma separated list of deployment names which should always be scaled to 0
	// for development.
	DebugDeploymentsAnnotation = "hypershift.openshift.io/debug-deployments"
)

// ParseNamespacedName expects a string with the format "namespace/name"
// and returns the proper types.NamespacedName.
// This is useful when watching a CR annotated with the format above to requeue the CR
// described in the annotation.
func ParseNamespacedName(name string) types.NamespacedName {
	parts := strings.SplitN(name, string(types.Separator), 2)
	if len(parts) > 1 {
		return types.NamespacedName{Namespace: parts[0], Name: parts[1]}
	}
	return types.NamespacedName{Name: parts[0]}
}

// CopyConfigMap copies the .Data field of configMap `source` into configmap `cm`
func CopyConfigMap(cm, source *corev1.ConfigMap) {
	cm.Data = map[string]string{}
	for k, v := range source.Data {
		cm.Data[k] = v
	}
}

func DeleteIfNeeded(ctx context.Context, c client.Client, o client.Object) (exists bool, err error) {
	if err := c.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("error getting %T: %w", o, err)
	}
	if o.GetDeletionTimestamp() != nil {
		return true, nil
	}
	if err := c.Delete(ctx, o); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("error deleting %T: %w", o, err)
	}

	return true, nil
}

// CompressAndEncode compresses and base-64 encodes a given byte array. Ideal for loading an
// arbitrary byte array into a ConfigMap or Secret.
func CompressAndEncode(payload []byte) (*bytes.Buffer, error) {
	out := bytes.NewBuffer(nil)

	if len(payload) == 0 {
		return out, nil
	}

	// We need to base64-encode our gzipped data so we can marshal it in and out
	// of a string since ConfigMaps and Secrets expect a textual representation.
	base64Enc := base64.NewEncoder(base64.StdEncoding, out)
	defer base64Enc.Close()

	err := compress(bytes.NewBuffer(payload), base64Enc)
	if err != nil {
		return nil, fmt.Errorf("could not compress and encode payload: %w", err)
	}

	err = base64Enc.Close()
	if err != nil {
		return nil, fmt.Errorf("could not close base64 encoder: %w", err)
	}

	return out, err
}

// Compress compresses a given byte array.
func Compress(payload []byte) (*bytes.Buffer, error) {
	in := bytes.NewBuffer(payload)
	out := bytes.NewBuffer(nil)

	if len(payload) == 0 {
		return out, nil
	}

	err := compress(in, out)
	return out, err
}

// DecodeAndDecompress decompresses and base-64 decodes a given byte array. Ideal for consuming a
// gzipped / base64-encoded byte array from a ConfigMap or Secret.
func DecodeAndDecompress(payload []byte) (*bytes.Buffer, error) {
	if len(payload) == 0 {
		return bytes.NewBuffer(nil), nil
	}

	base64Dec := base64.NewDecoder(base64.StdEncoding, bytes.NewReader(payload))

	return decompress(base64Dec)
}

// Compresses a given io.Reader to a given io.Writer
func compress(r io.Reader, w io.Writer) error {
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("could not initialize gzip writer: %w", err)
	}

	defer gz.Close()

	if _, err := io.Copy(gz, r); err != nil {
		return fmt.Errorf("could not compress payload: %w", err)
	}

	if err := gz.Close(); err != nil {
		return fmt.Errorf("could not close gzipwriter: %w", err)
	}

	return nil
}

// Decompresses a given io.Reader.
func decompress(r io.Reader) (*bytes.Buffer, error) {
	gz, err := gzip.NewReader(r)

	if err != nil {
		return bytes.NewBuffer(nil), fmt.Errorf("could not initialize gzip reader: %w", err)
	}

	defer gz.Close()

	data, err := io.ReadAll(gz)
	if err != nil {
		return bytes.NewBuffer(nil), fmt.Errorf("could not decompress payload: %w", err)
	}

	return bytes.NewBuffer(data), nil
}

// ResolveDNSHostname receives a hostname string and tries to resolve it.
// Returns error if the host can't be resolved.
func ResolveDNSHostname(ctx context.Context, hostName string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIPAddr(timeoutCtx, hostName)
	if err == nil && len(ips) == 0 {
		err = fmt.Errorf("couldn't resolve %s", hostName)
	}

	return err
}

// HashStruct takes a value, typically a string, and returns a 32-bit FNV-1a hashed version of the value as a string
func HashStruct(o interface{}) string {
	hash := fnv.New32a()
	hash.Write([]byte(fmt.Sprintf("%v", o)))
	intHash := hash.Sum32()
	return fmt.Sprintf("%08x", intHash)
}
