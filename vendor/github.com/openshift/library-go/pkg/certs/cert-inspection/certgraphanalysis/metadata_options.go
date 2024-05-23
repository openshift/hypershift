package certgraphanalysis

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/library-go/pkg/certs/cert-inspection/certgraphapi"
	corev1 "k8s.io/api/core/v1"
)

const rewritePrefix = "rewritten.cert-info.openshift.io/"

type configMapRewriteFunc func(configMap *corev1.ConfigMap)
type secretRewriteFunc func(secret *corev1.Secret)
type caBundleRewriteFunc func(metadata metav1.ObjectMeta, caBundle *certgraphapi.CertificateAuthorityBundle)
type certKeyPairRewriteFunc func(metadata metav1.ObjectMeta, certKeyPair *certgraphapi.CertKeyPair)
type pathRewriteFunc func(path string) string

type metadataOptions struct {
	rewriteCABundleFn    caBundleRewriteFunc
	rewriteCertKeyPairFn certKeyPairRewriteFunc
	rewriteConfigMapFn   configMapRewriteFunc
	rewriteSecretFn      secretRewriteFunc
	rewritePathFn        pathRewriteFunc
}

var (
	_                    configMapRewriter           = &metadataOptions{}
	_                    secretRewriter              = &metadataOptions{}
	_                    caBundleMetadataRewriter    = &metadataOptions{}
	_                    certKeypairMetadataRewriter = &metadataOptions{}
	revisionedPathReg, _                             = regexp.Compile(`-\d+$`)
	timestampReg, _                                  = regexp.Compile(`[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9]{2}.pem$`)
)

func (*metadataOptions) approved() {}

func (o *metadataOptions) rewriteCABundle(metadata metav1.ObjectMeta, caBundle *certgraphapi.CertificateAuthorityBundle) {
	if o.rewriteCABundleFn == nil {
		return
	}
	o.rewriteCABundleFn(metadata, caBundle)
}

func (o *metadataOptions) rewriteCertKeyPair(metadata metav1.ObjectMeta, certKeyPair *certgraphapi.CertKeyPair) {
	if o.rewriteCertKeyPairFn == nil {
		return
	}
	o.rewriteCertKeyPairFn(metadata, certKeyPair)
}

func (o *metadataOptions) rewriteConfigMap(configMap *corev1.ConfigMap) {
	if o.rewriteConfigMapFn == nil {
		return
	}
	o.rewriteConfigMapFn(configMap)
}

func (o *metadataOptions) rewriteSecret(secret *corev1.Secret) {
	if o.rewriteSecretFn == nil {
		return
	}
	o.rewriteSecretFn(secret)
}

func (o *metadataOptions) rewritePath(path string) string {
	if o.rewritePathFn == nil {
		return path
	}
	return o.rewritePathFn(path)
}

var (
	ElideProxyCADetails = &metadataOptions{
		rewriteCABundleFn: func(metadata metav1.ObjectMeta, caBundle *certgraphapi.CertificateAuthorityBundle) {
			isProxyCA := false
			if metadata.Namespace == "openshift-config-managed" && metadata.Name == "trusted-ca-bundle" {
				isProxyCA = true
			}
			// this plugin does a direct copy
			if metadata.Namespace == "openshift-cloud-controller-manager" && metadata.Name == "ccm-trusted-ca" {
				isProxyCA = true
			}
			// this namespace appears to hash (notice trailing dash) the content and lose labels
			if metadata.Namespace == "openshift-monitoring" && strings.Contains(metadata.Name, "-trusted-ca-bundle-") {
				isProxyCA = true
			}
			if len(metadata.Labels["config.openshift.io/inject-trusted-cabundle"]) > 0 {
				isProxyCA = true
			}

			if !isProxyCA {
				return
			}
			if len(caBundle.Spec.CertificateMetadata) < 10 {
				return
			}
			caBundle.Name = "proxy-ca"
			caBundle.LogicalName = "proxy-ca"
			caBundle.Spec.CertificateMetadata = []certgraphapi.CertKeyMetadata{
				{
					CertIdentifier: certgraphapi.CertIdentifier{
						CommonName:   "synthetic-proxy-ca",
						SerialNumber: "0",
						Issuer:       nil,
					},
				},
			}
		},
	}
	SkipRevisionedLocations = &metadataOptions{
		rewriteCABundleFn: func(metadata metav1.ObjectMeta, caBundle *certgraphapi.CertificateAuthorityBundle) {
			locations := []certgraphapi.OnDiskLocation{}
			for _, loc := range caBundle.Spec.OnDiskLocations {
				if skipRevisionedInOnDiskLocation(loc) {
					continue
				}
				locations = append(locations, loc)
			}
			caBundle.Spec.OnDiskLocations = locations
		},
		rewriteCertKeyPairFn: func(metadata metav1.ObjectMeta, certKeyPair *certgraphapi.CertKeyPair) {
			locations := []certgraphapi.OnDiskCertKeyPairLocation{}
			for _, loc := range certKeyPair.Spec.OnDiskLocations {
				// If either of cert or key is revisioned skip the entire location
				if len(loc.Cert.Path) != 0 && skipRevisionedInOnDiskLocation(loc.Cert) {
					continue
				}
				if len(loc.Key.Path) != 0 && skipRevisionedInOnDiskLocation(loc.Key) {
					continue
				}
				locations = append(locations, loc)
			}
			certKeyPair.Spec.OnDiskLocations = locations
		},
	}
	StripTimestamps = &metadataOptions{
		rewritePathFn: func(path string) string {
			return timestampReg.ReplaceAllString(path, "<timestamp>.pem")
		},
	}
)

// skipRevisionedInOnDiskLocation returns true if location is for revisioned certificate and needs to be skipped
func skipRevisionedInOnDiskLocation(location certgraphapi.OnDiskLocation) bool {
	if len(location.Path) == 0 {
		fmt.Fprintf(os.Stdout, "Skipping %s: empty path\n", location.Path)
		return true
	}
	parts := strings.Split(location.Path, "/")
	for _, part := range parts {
		if revisionedPathReg.MatchString(part) {
			fmt.Fprintf(os.Stdout, "Skipping %s: matched regexp in %s\n", location.Path, part)
			return true
		}
	}
	return false
}

func RewriteNodeIPs(nodeList []*corev1.Node) *metadataOptions {
	nodes := map[string]int{}
	for i, node := range nodeList {
		nodes[node.Name] = i
	}
	return &metadataOptions{
		rewriteSecretFn: func(secret *corev1.Secret) {
			for nodeName, masterID := range nodes {
				name := strings.ReplaceAll(secret.Name, nodeName, fmt.Sprintf("<master-%d>", masterID))
				if secret.Name != name {
					secret.Name = name
					if len(secret.Annotations) == 0 {
						secret.Annotations = map[string]string{}
					}
					// Replace node name from annotation value
					for key, value := range secret.Annotations {
						newValue := strings.ReplaceAll(value, nodeName, fmt.Sprintf("<master-%d>", masterID))
						if value != newValue {
							secret.Annotations[key] = newValue
						}
					}
					secret.Annotations[rewritePrefix+"RewriteNodeIPs"] = nodeName
				}
			}
		},
		rewritePathFn: func(path string) string {
			for nodeName, masterID := range nodes {
				newPath := strings.ReplaceAll(path, nodeName, fmt.Sprintf("<master-%d>", masterID))
				if newPath != path {
					fmt.Fprintf(os.Stdout, "Rewrote %s as %s\n", path, newPath)
					return newPath
				}
			}
			return path
		},
	}
}

func StripRootFSMountPoint(rootfsMount string) *metadataOptions {
	return &metadataOptions{
		rewritePathFn: func(path string) string {
			newPath := strings.ReplaceAll(path, rootfsMount, "")
			if newPath != path {
				fmt.Fprintf(os.Stdout, "Rewrote %s as %s\n", path, newPath)
				return newPath
			}
			return path
		},
	}
}
