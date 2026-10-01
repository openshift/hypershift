package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestScanArtifacts(t *testing.T) {
	objects := `apiVersion: v1
kind: Pod
metadata:
  name: control-plane
spec:
  initContainers:
  - name: init
    env:
    - name: AWS_ROLE_ARN
      value: arn:aws:iam::123456789012:role/PodRole
  containers:
  - name: manager
    env:
    - name: AWS_ROLE_ARN
      value: arn:aws:iam::123456789012:role/PodRole
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: controller
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/ServiceAccountRole
`
	for _, tc := range []struct {
		name        string
		kmsRegion   string
		wantRegions []string
	}{
		{
			name:        "When HostedCluster has a distinct KMS region, it should collect both regions and the KMS role",
			kmsRegion:   "us-east-2",
			wantRegions: []string{"us-east-2", "us-west-2"},
		},
		{
			name:        "When HostedCluster has no KMS configuration, it should collect platform and workload inventory",
			wantRegions: []string{"us-west-2"},
		},
		{
			name:        "When HostedCluster repeats the platform region for KMS, it should deduplicate regions",
			kmsRegion:   "us-west-2",
			wantRegions: []string{"us-west-2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			hostedCluster := hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{
					APIVersion: hyperv1.GroupVersion.String(),
					Kind:       "HostedCluster",
				},
				ObjectMeta: metav1.ObjectMeta{Name: "sample"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-west-2",
							RolesRef: hyperv1.AWSRolesRef{
								IngressARN: "arn:aws:iam::123456789012:role/Ingress",
								StorageARN: "arn:aws:iam::123456789012:role/Storage",
							},
						},
					},
				},
			}
			wantRoles := []string{
				"arn:aws:iam::123456789012:role/Ingress",
				"arn:aws:iam::123456789012:role/PodRole",
				"arn:aws:iam::123456789012:role/ServiceAccountRole",
				"arn:aws:iam::123456789012:role/Storage",
			}
			if tc.kmsRegion != "" {
				hostedCluster.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
					Type: hyperv1.KMS,
					KMS: &hyperv1.KMSSpec{
						Provider: hyperv1.AWS,
						AWS: &hyperv1.AWSKMSSpec{
							Region: tc.kmsRegion,
							Auth: hyperv1.AWSKMSAuthSpec{
								AWSKMSRoleARN: "arn:aws:iam::123456789012:role/KMS",
							},
						},
					},
				}
				wantRoles = append(wantRoles, "arn:aws:iam::123456789012:role/KMS")
				sort.Strings(wantRoles)
			}
			data, err := json.Marshal(hostedCluster)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "hostedcluster.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "resources.yaml"), []byte(objects), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("not a manifest"), 0o600); err != nil {
				t.Fatal(err)
			}

			inventory, err := scanArtifacts(root)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(inventory.RoleARNs, wantRoles) {
				t.Fatalf("unexpected roles: got %v, want %v", inventory.RoleARNs, wantRoles)
			}
			if !reflect.DeepEqual(inventory.Regions, tc.wantRegions) {
				t.Fatalf("unexpected regions: got %v, want %v", inventory.Regions, tc.wantRegions)
			}
		})
	}

	t.Run("When a manifest is malformed, it should return an error", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "broken.yaml"), []byte("kind: ["), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := scanArtifacts(root); err == nil {
			t.Fatal("expected malformed YAML error")
		}
	})
}

func TestCollectObject(t *testing.T) {
	t.Run("When a ServiceAccount annotation is not a role ARN, it should ignore the annotation", func(t *testing.T) {
		roles := map[string]struct{}{}
		collectObject(map[string]interface{}{
			"kind": "ServiceAccount",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					irsaRoleAnnotation: "not-an-arn",
				},
			},
		}, roles, map[string]struct{}{})
		if len(roles) != 0 {
			t.Fatalf("expected no role ARNs, got %v", roles)
		}
	})
}
