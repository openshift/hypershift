package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScanArtifacts(t *testing.T) {
	root := t.TempDir()
	objects := `apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: sample
spec:
  platform:
    aws:
      region: us-west-2
      rolesRef:
        ingressARN: arn:aws:iam::123456789012:role/Ingress
        storageARN: arn:aws:iam::123456789012:role/Storage
  secretEncryption:
    type: KMS
    kms:
      aws:
        auth:
          awsKMSRoleARN: arn:aws:iam::123456789012:role/KMS
---
apiVersion: v1
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
	wantRoles := []string{
		"arn:aws:iam::123456789012:role/Ingress",
		"arn:aws:iam::123456789012:role/KMS",
		"arn:aws:iam::123456789012:role/PodRole",
		"arn:aws:iam::123456789012:role/ServiceAccountRole",
		"arn:aws:iam::123456789012:role/Storage",
	}
	if !reflect.DeepEqual(inventory.RoleARNs, wantRoles) {
		t.Fatalf("unexpected roles: got %v, want %v", inventory.RoleARNs, wantRoles)
	}
	if !reflect.DeepEqual(inventory.Regions, []string{"us-west-2"}) {
		t.Fatalf("unexpected regions: got %v", inventory.Regions)
	}
}

func TestScanArtifactsMalformedManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.yaml"), []byte("kind: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := scanArtifacts(root); err == nil {
		t.Fatal("expected malformed YAML error")
	}
}

func TestCollectObjectIgnoresNonRoleARN(t *testing.T) {
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
}
