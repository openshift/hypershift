// Package velerocreds parses Azure credentials that originate from a Velero
// BackupStorageLocation (BSL) secret.
//
// Velero's own credential loader reads BSL credential files as dotenv
// (KEY=value) text regardless of the secret's data key name, and never as JSON.
// The HyperShift etcd-backup flow copies these BSL secrets verbatim, so the
// credential bytes reaching both the HCPEtcdBackup controller and the
// etcd-upload container are dotenv. This package centralizes that dotenv parsing
// so the controller (mode classification) and the uploader (credential
// construction) share one implementation instead of duplicating line parsing.
//
// It is intentionally dependency-free (standard library only) so that lean
// consumers such as the etcd-upload binary do not have to pull in the heavier
// support/azureutil package (and its controller-runtime / armnetwork deps).
package velerocreds

import "strings"

// DefaultCloudName is the Azure cloud environment assumed when a Velero
// credential blob does not specify AZURE_CLOUD_NAME.
const DefaultCloudName = "AzurePublicCloud"

// Credentials holds the Azure fields extracted from a Velero dotenv credential
// blob. Any field may be empty: callers validate according to their own needs
// (mode classification only inspects field presence, whereas credential
// construction requires ClientID, ClientSecret and TenantID).
type Credentials struct {
	ClientID     string
	ClientSecret string
	TenantID     string
	// CloudName is the value of AZURE_CLOUD_NAME, defaulting to DefaultCloudName
	// when the blob omits it. It is captured (rather than discarded) so callers
	// can make cloud-aware decisions; today the etcd-backup flow only supports
	// the public cloud.
	CloudName string
}

// ParseDotenv extracts Azure credential fields from Velero dotenv content.
//
// It is deliberately lenient: it never returns an error and simply reports
// whichever fields are present, so that callers performing mode classification
// can inspect presence without failing. Lines are trimmed before matching, so
// surrounding whitespace and CRLF line endings are tolerated; values may contain
// '=' (e.g. base64 secrets) because only the leading key prefix is stripped.
// The last occurrence of a duplicated key wins.
func ParseDotenv(data []byte) Credentials {
	creds := Credentials{CloudName: DefaultCloudName}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "AZURE_CLIENT_ID="); ok {
			creds.ClientID = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "AZURE_CLIENT_SECRET="); ok {
			creds.ClientSecret = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "AZURE_TENANT_ID="); ok {
			creds.TenantID = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "AZURE_CLOUD_NAME="); ok {
			if v = strings.TrimSpace(v); v != "" {
				creds.CloudName = v
			}
		}
	}
	return creds
}
