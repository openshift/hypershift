//go:build e2ev2

package main

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

type journalCloudFake struct {
	pages                         []*ec2.DescribeInstancesOutput
	describeError, authorizeError error
	described                     []*ec2.DescribeInstancesInput
	authorized                    []*ec2.AuthorizeSecurityGroupIngressInput
}

func (cloud *journalCloudFake) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	cloud.described = append(cloud.described, input)
	if cloud.describeError != nil {
		return nil, cloud.describeError
	}
	page := cloud.pages[0]
	cloud.pages = cloud.pages[1:]
	return page, nil
}

func (cloud *journalCloudFake) AuthorizeSecurityGroupIngress(_ context.Context, input *ec2.AuthorizeSecurityGroupIngressInput, _ ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	cloud.authorized = append(cloud.authorized, input)
	return &ec2.AuthorizeSecurityGroupIngressOutput{}, cloud.authorizeError
}

func TestJournalInstances(t *testing.T) {
	t.Run("When instances span pages, it should retain only running workers and exclude the bastion", func(t *testing.T) {
		cloud := &journalCloudFake{pages: []*ec2.DescribeInstancesOutput{
			{Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{{InstanceId: aws.String("worker"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}}, {State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameStopped}}, {}}}}, NextToken: aws.String("next")},
			{Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{{InstanceId: aws.String("bastion"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}, Tags: []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("infra-bastion")}}}}}}},
		}}
		instances, err := journalInstances(t.Context(), cloud, "infra")
		if err != nil || len(instances) != 1 || aws.ToString(instances[0].InstanceId) != "worker" || len(cloud.described) != 2 || aws.ToString(cloud.described[1].NextToken) != "next" {
			t.Fatalf("instances=%v err=%v requests=%v", instances, err, cloud.described)
		}
		if !reflect.DeepEqual(cloud.described[0].Filters, []ec2types.Filter{{Name: aws.String("tag:kubernetes.io/cluster/infra"), Values: []string{"owned"}}}) {
			t.Fatal("instance filter is not scoped to HC ownership")
		}
	})
	t.Run("When EC2 discovery fails, it should retain the API error", func(t *testing.T) {
		original := errors.New("EC2 failure")
		_, err := journalInstances(t.Context(), &journalCloudFake{describeError: original}, "infra")
		if !errors.Is(err, original) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestAuthorizeJournalSSH(t *testing.T) {
	for _, testCase := range []struct {
		name, cidr string
		groups     []ec2types.GroupIdentifier
		apiError   error
		wantError  bool
	}{
		{name: "When public workers lack a security group, it should fail without mutating AWS", wantError: true},
		{name: "When a configured source CIDR exists, it should authorize only that CIDR", cidr: "192.0.2.0/24", groups: []ec2types.GroupIdentifier{{GroupId: aws.String("sg-worker")}}},
		{name: "When the ingress rule already exists, it should tolerate only the duplicate error", groups: []ec2types.GroupIdentifier{{GroupId: aws.String("sg-worker")}}, apiError: &smithy.GenericAPIError{Code: "InvalidPermission.Duplicate"}},
		{name: "When AWS denies ingress authorization, it should retain the error", groups: []ec2types.GroupIdentifier{{GroupId: aws.String("sg-worker")}}, apiError: errors.New("denied"), wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("SSH_SOURCE_CIDR", testCase.cidr)
			cloud := &journalCloudFake{authorizeError: testCase.apiError}
			err := authorizeJournalSSH(t.Context(), cloud, testCase.groups)
			if (err != nil) != testCase.wantError {
				t.Fatalf("err=%v", err)
			}
			if len(testCase.groups) == 0 {
				if len(cloud.authorized) != 0 {
					t.Fatal("AWS called without security group")
				}
				return
			}
			request := cloud.authorized[0]
			cidr := testCase.cidr
			if cidr == "" {
				cidr = "0.0.0.0/0"
			}
			if aws.ToString(request.GroupId) != "sg-worker" || len(request.IpPermissions) != 1 || aws.ToInt32(request.IpPermissions[0].FromPort) != 22 || aws.ToInt32(request.IpPermissions[0].ToPort) != 22 || aws.ToString(request.IpPermissions[0].IpRanges[0].CidrIp) != cidr {
				t.Fatalf("invalid ingress: %#v", request)
			}
		})
	}
}

func TestCollectAWSJournals(t *testing.T) {
	for _, testCase := range []struct {
		name                                                                                    string
		public, missingKey, createFailure, copyFailure, discoveryFailure, empty, cleanupFailure bool
	}{
		{name: "When workers are private, it should collect gzipped journals through a tagged bastion and clean it up"},
		{name: "When workers have public IPs, it should avoid creating a bastion", public: true},
		{name: "When no SSH key exists, it should fail before creating cloud resources", missingKey: true},
		{name: "When bastion creation partially fails, it should still attempt bounded cleanup", createFailure: true},
		{name: "When journal copying is canceled, it should retain partial artifacts and still clean up", copyFailure: true},
		{name: "When instance discovery fails, it should clean up and retain that error", discoveryFailure: true},
		{name: "When no workers exist, it should skip copying but still clean up", empty: true},
		{name: "When bastion cleanup fails, it should retain that diagnostic error", cleanupFailure: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "hosted", Namespace: "clusters", Annotations: map[string]string{}}, Spec: hyperv1.HostedClusterSpec{InfraID: "infra", SSHKey: corev1.LocalObjectReference{Name: "ssh"}}}
			if testCase.public {
				cluster.Annotations[hyperv1.AWSMachinePublicIPs] = "true"
			}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "ssh", Namespace: "clusters"}, Data: map[string][]byte{"id_rsa": []byte("test-placeholder-not-a-real-key")}}
			if testCase.missingKey {
				secret.Data = nil
			}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(secret).Build()
			instances := []ec2types.Instance{{InstanceId: aws.String("i-worker"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}, PrivateIpAddress: aws.String("10.0.0.2"), PublicIpAddress: aws.String("192.0.2.2"), SecurityGroups: []ec2types.GroupIdentifier{{GroupId: aws.String("sg-worker")}}}}
			if testCase.empty {
				instances = nil
			}
			cloud := &journalCloudFake{pages: []*ec2.DescribeInstancesOutput{{Reservations: []ec2types.Reservation{{Instances: instances}}}}}
			original := errors.New("diagnostic failure")
			if testCase.discoveryFailure {
				cloud.describeError = original
			}
			created, destroyed, copied := 0, 0, 0
			var keyPath string
			directory := t.TempDir()
			err := collectAWSJournals(ctx, cluster, client, cloud, directory, journalOperations{
				createBastion: func(context.Context, logr.Logger) (string, error) {
					created++
					if testCase.createFailure {
						return "", original
					}
					return "192.0.2.10", nil
				},
				destroyBastion: func(ctx context.Context, _ logr.Logger) error {
					destroyed++
					if ctx.Err() != nil {
						t.Fatal("cleanup inherited canceled context")
					}
					deadline, bounded := ctx.Deadline()
					if !bounded || time.Until(deadline) > 5*time.Minute {
						t.Fatal("unbounded cleanup")
					}
					if testCase.cleanupFailure {
						return original
					}
					return nil
				},
				copyJournal: func(_ context.Context, address, bastion, key string, stdout, _ io.Writer) error {
					copied++
					keyPath = key
					checkJournalCopy(t, testCase.public, address, bastion, key)
					_, _ = io.WriteString(stdout, "journal content\n")
					if testCase.copyFailure {
						cancel()
						return original
					}
					return nil
				},
			})
			wantError := testCase.missingKey || testCase.createFailure || testCase.copyFailure || testCase.discoveryFailure || testCase.cleanupFailure
			if (err != nil) != wantError {
				t.Fatalf("err=%v", err)
			}
			if wantError && !testCase.missingKey && !errors.Is(err, original) {
				t.Fatalf("original diagnostic error lost: %v", err)
			}
			wantBastion := 1
			if testCase.public || testCase.missingKey {
				wantBastion = 0
			}
			if created != wantBastion || destroyed != wantBastion {
				t.Fatalf("created=%d destroyed=%d", created, destroyed)
			}
			wantCopied := 1
			if testCase.missingKey || testCase.createFailure || testCase.discoveryFailure || testCase.empty {
				wantCopied = 0
			}
			if copied != wantCopied {
				t.Fatalf("copied=%d want=%d", copied, wantCopied)
			}
			checkJournalArtifacts(t, testCase.public, testCase.copyFailure, copied, keyPath, directory)
		})
	}
}

func TestCollectAWSJournalsPrivateDirectoryCleanup(t *testing.T) {
	for _, failDiscovery := range []bool{false, true} {
		t.Run(fmt.Sprintf("When temporary key removal fails after discovery failure=%v, it should retain both errors without exposing paths", failDiscovery), func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "hosted", Namespace: "clusters", Annotations: map[string]string{hyperv1.AWSMachinePublicIPs: "true"}}, Spec: hyperv1.HostedClusterSpec{InfraID: "infra", SSHKey: corev1.LocalObjectReference{Name: "ssh"}}}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "ssh", Namespace: "clusters"}, Data: map[string][]byte{"id_rsa": []byte("placeholder-not-a-key")}}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(secret).Build()
			cloud := &journalCloudFake{pages: []*ec2.DescribeInstancesOutput{{}}}
			original := errors.New("discovery failure")
			if failDiscovery {
				cloud.describeError = original
			}
			removed := ""
			err := collectAWSJournals(t.Context(), cluster, client, cloud, t.TempDir(), journalOperations{
				removePrivateDirectory: func(directory string) error {
					removed = directory
					if contents, err := os.ReadFile(filepath.Join(directory, "id_rsa")); err != nil || string(contents) != "placeholder-not-a-key" {
						t.Fatalf("cleanup did not receive owned key: %v", err)
					}
					t.Cleanup(func() {
						if err := os.RemoveAll(directory); err != nil {
							t.Error(err)
						}
					})
					return &os.PathError{Op: "remove", Path: directory, Err: os.ErrPermission}
				},
			})
			if removed == "" || !errors.Is(err, os.ErrPermission) || errors.Is(err, original) != failDiscovery {
				t.Fatalf("cleanup/discovery errors lost: %v", err)
			}
			if strings.Contains(err.Error(), removed) || strings.Contains(err.Error(), "placeholder-not-a-key") {
				t.Fatal("cleanup error exposed private path or contents")
			}
		})
	}
}

func checkJournalCopy(t *testing.T, public bool, address, bastion, key string) {
	t.Helper()
	wantIP, wantBastion := "10.0.0.2", "192.0.2.10"
	if public {
		wantIP, wantBastion = "192.0.2.2", ""
	}
	if address != wantIP || bastion != wantBastion {
		t.Fatalf("address=%s bastion=%s", address, bastion)
	}
	stat, err := os.Stat(key)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("key permissions: %v %v", stat, err)
	}
}

func checkJournalArtifacts(t *testing.T, public, copyFailure bool, copied int, keyPath, directory string) {
	t.Helper()
	if keyPath != "" {
		if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("temporary private key leaked")
		}
	}
	if copied == 0 {
		return
	}
	address := "10.0.0.2"
	if public {
		address = "192.0.2.2"
	}
	file, err := os.Open(filepath.Join(directory, "machine-journals", "journal_"+address+".log.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "journal content\n" {
		t.Fatalf("journal=%q error=%v", data, err)
	}
	if copyFailure {
		if _, err := os.Stat(filepath.Join(directory, "machine-journals", "instance-i-worker.txt")); err != nil {
			t.Fatal("instance fallback missing")
		}
	}
}

func TestCopyAWSJournal(t *testing.T) {
	t.Run("When copying via a bastion, it should pass identity and journal options without an agent or private key in artifacts", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "ssh"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", directory)
		var output strings.Builder
		if err := copyAWSJournal(t.Context(), "10.0.0.2", "192.0.2.10", "/tmp/journal-key", &output, io.Discard); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"ProxyCommand=ssh", "/tmp/journal-key", "ec2-user@192.0.2.10", "core@10.0.0.2", "sudo journalctl --no-pager", "BatchMode=yes"} {
			if !strings.Contains(output.String(), expected) {
				t.Fatalf("missing %q in %s", expected, &output)
			}
		}
	})
	t.Run("When cancellation occurs, it should not invoke SSH", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := copyAWSJournal(ctx, "10.0.0.2", "", "/tmp/journal-key", io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestDumpAWSConsole(t *testing.T) {
	t.Run("When kubeconfig is unavailable, it should return before accessing AWS", func(t *testing.T) {
		config := filepath.Join(t.TempDir(), "invalid-kubeconfig")
		if err := os.WriteFile(config, []byte("invalid: ["), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KUBECONFIG", config)
		if err := dumpAWSConsole(t.Context(), lifecycle.ClusterEntry{Name: "hosted", Namespace: "clusters"}, t.TempDir()); err == nil {
			t.Fatal("missing management config ignored")
		}
	})
}

func TestDumpAWSJournals(t *testing.T) {
	t.Run("When kubeconfig is unavailable, it should return before accessing AWS", func(t *testing.T) {
		config := filepath.Join(t.TempDir(), "invalid-kubeconfig")
		if err := os.WriteFile(config, []byte("invalid: ["), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KUBECONFIG", config)
		if err := dumpAWSJournals(t.Context(), lifecycle.ClusterEntry{Name: "hosted", Namespace: "clusters"}, t.TempDir()); err == nil {
			t.Fatal("missing management config ignored")
		}
	})
}

func TestTeardownAWSCredentialsFile(t *testing.T) {
	for _, testCase := range []struct{ name, guest, shared, want string }{
		{name: "When guest infrastructure credentials are configured, it should use them for console and journals", guest: "/guest/credentials", shared: "/shared/credentials", want: "/guest/credentials"},
		{name: "When only standard AWS credentials are configured, it should preserve the CLI credential source", shared: "/shared/credentials", want: "/shared/credentials"},
		{name: "When no credentials file is configured, it should leave the production SDK default chain enabled"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("AWS_GUEST_INFRA_CREDENTIALS_FILE", testCase.guest)
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", testCase.shared)
			if got := teardownAWSCredentialsFile(); got != testCase.want {
				t.Fatalf("credentials path=%q want=%q", got, testCase.want)
			}
		})
	}
}
