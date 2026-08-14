package kms

import (
	"fmt"
	"path"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func validAzureKMSSpec() *hyperv1.AzureKMSSpec {
	return &hyperv1.AzureKMSSpec{
		ActiveKey: hyperv1.AzureKMSKey{
			KeyVaultName: "test-vault",
			KeyName:      "test-key",
			KeyVersion:   "1",
		},
	}
}

func validAzureKMSSpecWithBackup() *hyperv1.AzureKMSSpec {
	spec := validAzureKMSSpec()
	spec.BackupKey = &hyperv1.AzureKMSKey{ //nolint:staticcheck
		KeyVaultName: "test-vault",
		KeyName:      "backup-key",
		KeyVersion:   "1",
	}
	return spec
}

func newTestAzureKMSProvider(spec *hyperv1.AzureKMSSpec, image string, opts AzureKMSProviderOptions) (*azureKMSProvider, error) {
	var writeKey hyperv1.AzureKMSKey
	var readKey *hyperv1.AzureKMSKey
	if spec != nil {
		writeKey = spec.ActiveKey
		readKey = spec.BackupKey //nolint:staticcheck
	}
	return NewAzureKMSProvider(writeKey, readKey, spec, image, opts)
}

func TestNewAzureKMSProvider(t *testing.T) {
	tests := []struct {
		name        string
		kmsSpec     *hyperv1.AzureKMSSpec
		image       string
		opts        AzureKMSProviderOptions
		expectError bool
		errContains string
	}{
		{
			name:        "When kmsSpec is nil it should return an error",
			kmsSpec:     nil,
			image:       "test-image:latest",
			opts:        AzureKMSProviderOptions{},
			expectError: true,
			errContains: "azure kms metadata not specified",
		},
		{
			name:    "When self-managed with empty kmsClientID it should return an error",
			kmsSpec: validAzureKMSSpec(),
			image:   "test-image:latest",
			opts: AzureKMSProviderOptions{
				IsSelfManaged: true,
				KMSClientID:   "",
				TenantID:      "test-tenant-id",
			},
			expectError: true,
			errContains: "kmsClientID and tenantID are required",
		},
		{
			name:    "When self-managed with empty tenantID it should return an error",
			kmsSpec: validAzureKMSSpec(),
			image:   "test-image:latest",
			opts: AzureKMSProviderOptions{
				IsSelfManaged: true,
				KMSClientID:   "test-client-id",
				TenantID:      "",
			},
			expectError: true,
			errContains: "kmsClientID and tenantID are required",
		},
		{
			name:    "When self-managed with empty tokenMinterImage it should return an error",
			kmsSpec: validAzureKMSSpec(),
			image:   "test-image:latest",
			opts: AzureKMSProviderOptions{
				IsSelfManaged:    true,
				KMSClientID:      "test-client-id",
				TenantID:         "test-tenant-id",
				TokenMinterImage: "",
			},
			expectError: true,
			errContains: "tokenMinterImage is required",
		},
		{
			name:    "When managed Azure it should create provider successfully",
			kmsSpec: validAzureKMSSpec(),
			image:   "test-image:latest",
			opts: AzureKMSProviderOptions{
				IsSelfManaged: false,
			},
			expectError: false,
		},
		{
			name:    "When self-managed Azure with valid options it should create provider successfully",
			kmsSpec: validAzureKMSSpec(),
			image:   "test-image:latest",
			opts: AzureKMSProviderOptions{
				IsSelfManaged:    true,
				KMSClientID:      "test-client-id",
				TenantID:         "test-tenant-id",
				TokenMinterImage: "test-token-minter:latest",
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			provider, err := newTestAzureKMSProvider(tc.kmsSpec, tc.image, tc.opts)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.errContains))
				g.Expect(provider).To(BeNil())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(provider).NotTo(BeNil())
			}
		})
	}
}

func TestGenerateKMSPodConfig_SelfManaged(t *testing.T) {
	tests := []struct {
		name  string
		check func(g Gomega, podConfig *KMSPodConfig)
	}{
		{
			name: "When self-managed it should include token minter container with correct config",
			check: func(g Gomega, podConfig *KMSPodConfig) {
				var tokenMinter *containerInfo
				for i, c := range podConfig.Containers {
					if c.Name == "azure-kms-token-minter" {
						tokenMinter = &containerInfo{idx: i, container: c}
						break
					}
				}
				g.Expect(tokenMinter).NotTo(BeNil(), "expected token minter container to be present")
				g.Expect(tokenMinter.container.Command).To(Equal([]string{"/usr/bin/control-plane-operator", "token-minter"}))
				g.Expect(tokenMinter.container.Args).To(ContainElement("--token-audience=openshift"))
				g.Expect(tokenMinter.container.Args).To(ContainElement("--service-account-namespace=kube-system"))
				g.Expect(tokenMinter.container.Args).To(ContainElement("--service-account-name=kms-provider"))
				g.Expect(tokenMinter.container.Args).To(ContainElement(
					ContainSubstring("--token-file=" + path.Join(config.CloudTokenMountPath, "token")),
				))
				g.Expect(tokenMinter.container.Args).To(ContainElement(
					ContainSubstring("--kubeconfig=" + path.Join("/etc/kubernetes", podspec.KubeconfigKey)),
				))

				// Verify volume mounts
				volumeNames := make([]string, 0, len(tokenMinter.container.VolumeMounts))
				for _, vm := range tokenMinter.container.VolumeMounts {
					volumeNames = append(volumeNames, vm.Name)
				}
				g.Expect(volumeNames).To(ContainElement("azure-kms-cloud-token"))
				g.Expect(volumeNames).To(ContainElement(kasVolumeLocalhostKubeconfig))
			},
		},
		{
			name: "When self-managed it should include cloud-token emptyDir volume",
			check: func(g Gomega, podConfig *KMSPodConfig) {
				found := false
				for _, v := range podConfig.Volumes {
					if v.Name == "azure-kms-cloud-token" {
						found = true
						g.Expect(v.EmptyDir).NotTo(BeNil(), "expected cloud-token volume to be an emptyDir")
						break
					}
				}
				g.Expect(found).To(BeTrue(), "expected cloud-token volume to be present")
			},
		},
		{
			name: "When self-managed it should NOT include secret-store CSI volume",
			check: func(g Gomega, podConfig *KMSPodConfig) {
				for _, v := range podConfig.Volumes {
					g.Expect(v.Name).NotTo(Equal(config.ManagedAzureKMSSecretStoreVolumeName),
						"expected secret-store CSI volume to NOT be present in self-managed mode")
				}
			},
		},
		{
			name: "When self-managed the KMS container should mount cloud-token volume",
			check: func(g Gomega, podConfig *KMSPodConfig) {
				for _, c := range podConfig.Containers {
					if c.Name == "azure-kms-provider-active" {
						found := false
						for _, vm := range c.VolumeMounts {
							if vm.Name == "azure-kms-cloud-token" {
								found = true
								g.Expect(vm.MountPath).To(Equal(config.CloudTokenMountPath))
								g.Expect(vm.ReadOnly).To(BeTrue())
								break
							}
						}
						g.Expect(found).To(BeTrue(), "expected active KMS container to mount cloud-token volume")
						return
					}
				}
				g.Expect(false).To(BeTrue(), "active KMS container not found")
			},
		},
		{
			name: "When self-managed the KMS container should have workload identity env vars",
			check: func(g Gomega, podConfig *KMSPodConfig) {
				for _, c := range podConfig.Containers {
					if c.Name == "azure-kms-provider-active" {
						envMap := map[string]string{}
						for _, e := range c.Env {
							envMap[e.Name] = e.Value
						}
						g.Expect(envMap).To(HaveKeyWithValue("AZURE_CLIENT_ID", "test-client-id"))
						g.Expect(envMap).To(HaveKeyWithValue("AZURE_TENANT_ID", "test-tenant-id"))
						g.Expect(envMap).To(HaveKeyWithValue("AZURE_FEDERATED_TOKEN_FILE",
							path.Join(config.CloudTokenMountPath, "token")))
						return
					}
				}
				g.Expect(false).To(BeTrue(), "active KMS container not found")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			provider, err := newTestAzureKMSProvider(validAzureKMSSpec(), "test-kms-image:latest", AzureKMSProviderOptions{
				IsSelfManaged:    true,
				KMSClientID:      "test-client-id",
				TenantID:         "test-tenant-id",
				TokenMinterImage: "test-token-minter:latest",
			})
			g.Expect(err).NotTo(HaveOccurred())

			podConfig, err := provider.GenerateKMSPodConfig()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podConfig).NotTo(BeNil())

			tc.check(g, podConfig)
		})
	}
}

type containerInfo struct {
	idx       int
	container corev1.Container
}

func TestGenerateKMSPodConfig_Managed(t *testing.T) {
	tests := []struct {
		name  string
		check func(g Gomega, podConfig *KMSPodConfig)
	}{
		{
			name: "When managed it should NOT include token minter container",
			check: func(g Gomega, podConfig *KMSPodConfig) {
				for _, c := range podConfig.Containers {
					g.Expect(c.Name).NotTo(Equal("azure-kms-token-minter"),
						"expected token minter container to NOT be present in managed mode")
				}
			},
		},
		{
			name: "When managed it should include secret-store CSI volume",
			check: func(g Gomega, podConfig *KMSPodConfig) {
				found := false
				for _, v := range podConfig.Volumes {
					if v.Name == config.ManagedAzureKMSSecretStoreVolumeName {
						found = true
						g.Expect(v.CSI).NotTo(BeNil(), "expected secret-store volume to use CSI driver")
						g.Expect(v.CSI.Driver).To(Equal(config.ManagedAzureSecretsStoreCSIDriver))
						break
					}
				}
				g.Expect(found).To(BeTrue(), "expected secret-store CSI volume to be present")
			},
		},
		{
			name: "When managed it should NOT include cloud-token volume",
			check: func(g Gomega, podConfig *KMSPodConfig) {
				for _, v := range podConfig.Volumes {
					g.Expect(v.Name).NotTo(Equal("azure-kms-cloud-token"),
						"expected cloud-token volume to NOT be present in managed mode")
				}
			},
		},
		{
			name: "When managed the KMS container should NOT have workload identity env vars",
			check: func(g Gomega, podConfig *KMSPodConfig) {
				for _, c := range podConfig.Containers {
					if c.Name == "azure-kms-provider-active" {
						for _, e := range c.Env {
							g.Expect(e.Name).NotTo(Equal("AZURE_CLIENT_ID"),
								"expected managed KMS container to NOT have AZURE_CLIENT_ID env var")
						}
						return
					}
				}
				g.Expect(false).To(BeTrue(), "active KMS container not found")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			provider, err := newTestAzureKMSProvider(validAzureKMSSpec(), "test-kms-image:latest", AzureKMSProviderOptions{
				IsSelfManaged: false,
			})
			g.Expect(err).NotTo(HaveOccurred())

			podConfig, err := provider.GenerateKMSPodConfig()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(podConfig).NotTo(BeNil())

			tc.check(g, podConfig)
		})
	}
}

func TestGenerateKMSPodConfig_BackupKey(t *testing.T) {
	t.Run("When self-managed backup key is specified it should include backup KMS container with env vars", func(t *testing.T) {
		g := NewWithT(t)

		provider, err := newTestAzureKMSProvider(validAzureKMSSpecWithBackup(), "test-kms-image:latest", AzureKMSProviderOptions{
			IsSelfManaged:    true,
			KMSClientID:      "test-client-id",
			TenantID:         "test-tenant-id",
			TokenMinterImage: "test-token-minter:latest",
		})
		g.Expect(err).NotTo(HaveOccurred())

		podConfig, err := provider.GenerateKMSPodConfig()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(podConfig).NotTo(BeNil())

		found := false
		for _, c := range podConfig.Containers {
			if c.Name == "azure-kms-provider-backup" {
				found = true
				envMap := map[string]string{}
				for _, e := range c.Env {
					envMap[e.Name] = e.Value
				}
				g.Expect(envMap).To(HaveKeyWithValue("AZURE_CLIENT_ID", "test-client-id"))
				g.Expect(envMap).To(HaveKeyWithValue("AZURE_TENANT_ID", "test-tenant-id"))
				g.Expect(envMap).To(HaveKeyWithValue("AZURE_FEDERATED_TOKEN_FILE", path.Join(config.CloudTokenMountPath, "token")))
				break
			}
		}
		g.Expect(found).To(BeTrue(), "expected backup KMS container to be present when backup key is specified")
	})

	t.Run("When managed backup key is specified it should include backup container with secret-store mount", func(t *testing.T) {
		g := NewWithT(t)

		provider, err := newTestAzureKMSProvider(validAzureKMSSpecWithBackup(), "test-kms-image:latest", AzureKMSProviderOptions{
			IsSelfManaged: false,
		})
		g.Expect(err).NotTo(HaveOccurred())

		podConfig, err := provider.GenerateKMSPodConfig()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(podConfig).NotTo(BeNil())

		found := false
		for _, c := range podConfig.Containers {
			if c.Name == "azure-kms-provider-backup" {
				found = true
				hasSecretStore := false
				hasCloudToken := false
				for _, vm := range c.VolumeMounts {
					if vm.Name == config.ManagedAzureKMSSecretStoreVolumeName {
						hasSecretStore = true
					}
					if vm.Name == "azure-kms-cloud-token" {
						hasCloudToken = true
					}
				}
				g.Expect(hasSecretStore).To(BeTrue(), "expected backup KMS container to have secret-store mount")
				g.Expect(hasCloudToken).To(BeFalse(), "expected backup KMS container to NOT have cloud-token mount")
				break
			}
		}
		g.Expect(found).To(BeTrue(), "expected backup KMS container to be present when backup key is specified")
	})
}

func findContainer(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

func TestGenerateKMSPodConfig_ActiveContainerArgs(t *testing.T) {
	g := NewWithT(t)

	provider, err := newTestAzureKMSProvider(validAzureKMSSpec(), "test-kms-image:latest", AzureKMSProviderOptions{
		IsSelfManaged: false,
	})
	g.Expect(err).NotTo(HaveOccurred())

	podConfig, err := provider.GenerateKMSPodConfig()
	g.Expect(err).NotTo(HaveOccurred())

	active := findContainer(podConfig.Containers, "azure-kms-provider-active")
	g.Expect(active).NotTo(BeNil(), "expected active KMS container to exist")

	tests := []struct {
		name     string
		expected string
	}{
		{
			name:     "When active KMS container is created it should pass the key vault name",
			expected: "--keyvault-name=test-vault",
		},
		{
			name:     "When active KMS container is created it should pass the key name",
			expected: "--key-name=test-key",
		},
		{
			name:     "When active KMS container is created it should pass the key version",
			expected: "--key-version=1",
		},
		{
			name:     "When active KMS container is created it should listen on the active unix socket",
			expected: fmt.Sprintf("--listen-addr=unix:///opt/%s", azureActiveKMSUnixSocketFileName),
		},
		{
			name:     "When active KMS container is created it should use port 8787 for health checks",
			expected: fmt.Sprintf("--healthz-port=%d", azureActiveKMSHealthPort),
		},
		{
			name:     "When active KMS container is created it should expose metrics on port 8095",
			expected: fmt.Sprintf("--metrics-addr=%s", azureActiveKMSMetricsAddr),
		},
		{
			name:     "When active KMS container is created it should set the healthz path",
			expected: "--healthz-path=/healthz",
		},
		{
			name:     "When active KMS container is created it should point to the azure.json config file",
			expected: "--config-file-path=/etc/kubernetes/azure.json",
		},
		{
			name:     "When active KMS container is created it should enable verbose logging",
			expected: "-v=1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(active.Args).To(ContainElement(tc.expected))
		})
	}
}

func TestGenerateKMSPodConfig_BackupContainerArgs(t *testing.T) {
	g := NewWithT(t)

	provider, err := newTestAzureKMSProvider(validAzureKMSSpecWithBackup(), "test-kms-image:latest", AzureKMSProviderOptions{
		IsSelfManaged: false,
	})
	g.Expect(err).NotTo(HaveOccurred())

	podConfig, err := provider.GenerateKMSPodConfig()
	g.Expect(err).NotTo(HaveOccurred())

	backup := findContainer(podConfig.Containers, "azure-kms-provider-backup")
	g.Expect(backup).NotTo(BeNil(), "expected backup KMS container to exist")

	tests := []struct {
		name     string
		expected string
	}{
		{
			name:     "When backup KMS container is created it should pass the backup key vault name",
			expected: "--keyvault-name=test-vault",
		},
		{
			name:     "When backup KMS container is created it should pass the backup key name",
			expected: "--key-name=backup-key",
		},
		{
			name:     "When backup KMS container is created it should listen on the backup unix socket",
			expected: fmt.Sprintf("--listen-addr=unix:///opt/%s", azureBackupKMSUnixSocketFileName),
		},
		{
			name:     "When backup KMS container is created it should use port 8788 for health checks",
			expected: fmt.Sprintf("--healthz-port=%d", azureBackupKMSHealthPort),
		},
		{
			name:     "When backup KMS container is created it should expose metrics on port 8096",
			expected: fmt.Sprintf("--metrics-addr=%s", azureBackupKMSMetricsAddr),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(backup.Args).To(ContainElement(tc.expected))
		})
	}
}

func TestGenerateKMSPodConfig_LivenessProbe(t *testing.T) {
	tests := []struct {
		name        string
		containerFn func(podConfig *KMSPodConfig) *corev1.Container
		healthPort  int
	}{
		{
			name: "active KMS container",
			containerFn: func(podConfig *KMSPodConfig) *corev1.Container {
				return findContainer(podConfig.Containers, "azure-kms-provider-active")
			},
			healthPort: azureActiveKMSHealthPort,
		},
		{
			name: "backup KMS container",
			containerFn: func(podConfig *KMSPodConfig) *corev1.Container {
				return findContainer(podConfig.Containers, "azure-kms-provider-backup")
			},
			healthPort: azureBackupKMSHealthPort,
		},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("When %s is created it should have a correctly configured liveness probe", tc.name), func(t *testing.T) {
			g := NewWithT(t)

			provider, err := newTestAzureKMSProvider(validAzureKMSSpecWithBackup(), "test-kms-image:latest", AzureKMSProviderOptions{
				IsSelfManaged: false,
			})
			g.Expect(err).NotTo(HaveOccurred())

			podConfig, err := provider.GenerateKMSPodConfig()
			g.Expect(err).NotTo(HaveOccurred())

			c := tc.containerFn(podConfig)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.LivenessProbe).NotTo(BeNil(), "expected liveness probe to be configured")
			g.Expect(c.LivenessProbe.HTTPGet).NotTo(BeNil(), "expected HTTP GET probe handler")
			g.Expect(c.LivenessProbe.HTTPGet.Path).To(Equal("/healthz"))
			g.Expect(c.LivenessProbe.HTTPGet.Port.IntValue()).To(Equal(tc.healthPort))
			g.Expect(c.LivenessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTP))
			g.Expect(c.LivenessProbe.InitialDelaySeconds).To(Equal(int32(120)))
			g.Expect(c.LivenessProbe.PeriodSeconds).To(Equal(int32(300)))
			g.Expect(c.LivenessProbe.TimeoutSeconds).To(Equal(int32(160)))
			g.Expect(c.LivenessProbe.FailureThreshold).To(Equal(int32(3)))
			g.Expect(c.LivenessProbe.SuccessThreshold).To(Equal(int32(1)))
		})
	}
}

func TestGenerateKMSPodConfig_ResourceRequests(t *testing.T) {
	g := NewWithT(t)

	provider, err := newTestAzureKMSProvider(validAzureKMSSpec(), "test-kms-image:latest", AzureKMSProviderOptions{
		IsSelfManaged:    true,
		KMSClientID:      "test-client-id",
		TenantID:         "test-tenant-id",
		TokenMinterImage: "test-token-minter:latest",
	})
	g.Expect(err).NotTo(HaveOccurred())

	podConfig, err := provider.GenerateKMSPodConfig()
	g.Expect(err).NotTo(HaveOccurred())

	tests := []struct {
		name      string
		container string
		cpu       string
		memory    string
	}{
		{
			name:      "When active KMS container is created it should request 10m CPU and 10Mi memory",
			container: "azure-kms-provider-active",
			cpu:       "10m",
			memory:    "10Mi",
		},
		{
			name:      "When token minter container is created it should request 10m CPU and 30Mi memory",
			container: "azure-kms-token-minter",
			cpu:       "10m",
			memory:    "30Mi",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			c := findContainer(podConfig.Containers, tc.container)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Resources.Requests[corev1.ResourceCPU]).To(Equal(resource.MustParse(tc.cpu)))
			g.Expect(c.Resources.Requests[corev1.ResourceMemory]).To(Equal(resource.MustParse(tc.memory)))
		})
	}
}

func TestGenerateKMSPodConfig_VolumeMountPaths(t *testing.T) {
	tests := []struct {
		name          string
		isSelfManaged bool
		opts          AzureKMSProviderOptions
		checks        func(g Gomega, podConfig *KMSPodConfig)
	}{
		{
			name:          "When self-managed KMS container is created it should mount credentials at /etc/kubernetes and socket at /opt",
			isSelfManaged: true,
			opts: AzureKMSProviderOptions{
				IsSelfManaged:    true,
				KMSClientID:      "test-client-id",
				TenantID:         "test-tenant-id",
				TokenMinterImage: "test-token-minter:latest",
			},
			checks: func(g Gomega, podConfig *KMSPodConfig) {
				c := findContainer(podConfig.Containers, "azure-kms-provider-active")
				g.Expect(c).NotTo(BeNil())
				mountMap := map[string]string{}
				for _, vm := range c.VolumeMounts {
					mountMap[vm.Name] = vm.MountPath
				}
				g.Expect(mountMap).To(HaveKeyWithValue("azure-kms-credentials", "/etc/kubernetes"))
				g.Expect(mountMap).To(HaveKeyWithValue("kms-socket", "/opt"))
				g.Expect(mountMap).To(HaveKeyWithValue("azure-kms-cloud-token", config.CloudTokenMountPath))
			},
		},
		{
			name:          "When managed KMS container is created it should mount secret-store CSI volume at the certificate path",
			isSelfManaged: false,
			opts: AzureKMSProviderOptions{
				IsSelfManaged: false,
			},
			checks: func(g Gomega, podConfig *KMSPodConfig) {
				c := findContainer(podConfig.Containers, "azure-kms-provider-active")
				g.Expect(c).NotTo(BeNil())
				mountMap := map[string]string{}
				for _, vm := range c.VolumeMounts {
					mountMap[vm.Name] = vm.MountPath
				}
				g.Expect(mountMap).To(HaveKeyWithValue("azure-kms-credentials", "/etc/kubernetes"))
				g.Expect(mountMap).To(HaveKeyWithValue("kms-socket", "/opt"))
				g.Expect(mountMap).To(HaveKeyWithValue(config.ManagedAzureKMSSecretStoreVolumeName, config.ManagedAzureCertificateMountPath))
			},
		},
		{
			name:          "When token minter is created it should mount cloud-token and kubeconfig volumes",
			isSelfManaged: true,
			opts: AzureKMSProviderOptions{
				IsSelfManaged:    true,
				KMSClientID:      "test-client-id",
				TenantID:         "test-tenant-id",
				TokenMinterImage: "test-token-minter:latest",
			},
			checks: func(g Gomega, podConfig *KMSPodConfig) {
				c := findContainer(podConfig.Containers, "azure-kms-token-minter")
				g.Expect(c).NotTo(BeNil())
				mountMap := map[string]string{}
				for _, vm := range c.VolumeMounts {
					mountMap[vm.Name] = vm.MountPath
				}
				g.Expect(mountMap).To(HaveKeyWithValue("azure-kms-cloud-token", config.CloudTokenMountPath))
				g.Expect(mountMap).To(HaveKeyWithValue(kasVolumeLocalhostKubeconfig, "/etc/kubernetes"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			provider, err := newTestAzureKMSProvider(validAzureKMSSpec(), "test-kms-image:latest", tc.opts)
			g.Expect(err).NotTo(HaveOccurred())
			podConfig, err := provider.GenerateKMSPodConfig()
			g.Expect(err).NotTo(HaveOccurred())
			tc.checks(g, podConfig)
		})
	}
}

func TestGenerateKMSPodConfig_KASContainerMutation(t *testing.T) {
	t.Run("When KAS container mutation is applied it should mount the KMS socket volume at /opt", func(t *testing.T) {
		g := NewWithT(t)

		provider, err := newTestAzureKMSProvider(validAzureKMSSpec(), "test-kms-image:latest", AzureKMSProviderOptions{
			IsSelfManaged: false,
		})
		g.Expect(err).NotTo(HaveOccurred())

		podConfig, err := provider.GenerateKMSPodConfig()
		g.Expect(err).NotTo(HaveOccurred())

		kasContainer := &corev1.Container{Name: KasMainContainerName}
		podConfig.KASContainerMutate(kasContainer)

		mountMap := map[string]string{}
		for _, vm := range kasContainer.VolumeMounts {
			mountMap[vm.Name] = vm.MountPath
		}
		g.Expect(mountMap).To(HaveKeyWithValue("kms-socket", "/opt"))
	})
}

func TestGenerateKMSPodConfig_ContainerPorts(t *testing.T) {
	g := NewWithT(t)

	provider, err := newTestAzureKMSProvider(validAzureKMSSpecWithBackup(), "test-kms-image:latest", AzureKMSProviderOptions{
		IsSelfManaged: false,
	})
	g.Expect(err).NotTo(HaveOccurred())

	podConfig, err := provider.GenerateKMSPodConfig()
	g.Expect(err).NotTo(HaveOccurred())

	tests := []struct {
		name          string
		containerName string
		expectedPort  int32
	}{
		{
			name:          "When active KMS container is created it should expose health port 8787",
			containerName: "azure-kms-provider-active",
			expectedPort:  int32(azureActiveKMSHealthPort),
		},
		{
			name:          "When backup KMS container is created it should expose health port 8788",
			containerName: "azure-kms-provider-backup",
			expectedPort:  int32(azureBackupKMSHealthPort),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			c := findContainer(podConfig.Containers, tc.containerName)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.Ports).To(HaveLen(1))
			g.Expect(c.Ports[0].Name).To(Equal("http"))
			g.Expect(c.Ports[0].ContainerPort).To(Equal(tc.expectedPort))
			g.Expect(c.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
		})
	}
}

func TestGenerateKMSPodConfig_ImagePullPolicy(t *testing.T) {
	g := NewWithT(t)

	provider, err := newTestAzureKMSProvider(validAzureKMSSpec(), "test-kms-image:latest", AzureKMSProviderOptions{
		IsSelfManaged:    true,
		KMSClientID:      "test-client-id",
		TenantID:         "test-tenant-id",
		TokenMinterImage: "test-token-minter:latest",
	})
	g.Expect(err).NotTo(HaveOccurred())

	podConfig, err := provider.GenerateKMSPodConfig()
	g.Expect(err).NotTo(HaveOccurred())

	tests := []struct {
		name          string
		containerName string
	}{
		{
			name:          "When active KMS container is created it should use IfNotPresent pull policy",
			containerName: "azure-kms-provider-active",
		},
		{
			name:          "When token minter container is created it should use IfNotPresent pull policy",
			containerName: "azure-kms-token-minter",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			c := findContainer(podConfig.Containers, tc.containerName)
			g.Expect(c).NotTo(BeNil())
			g.Expect(c.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
		})
	}
}

func TestGenerateKMSPodConfig_NoBackupContainerWithoutBackupKey(t *testing.T) {
	t.Run("When no backup key is specified it should not create a backup container", func(t *testing.T) {
		g := NewWithT(t)

		provider, err := newTestAzureKMSProvider(validAzureKMSSpec(), "test-kms-image:latest", AzureKMSProviderOptions{
			IsSelfManaged: false,
		})
		g.Expect(err).NotTo(HaveOccurred())

		podConfig, err := provider.GenerateKMSPodConfig()
		g.Expect(err).NotTo(HaveOccurred())

		backup := findContainer(podConfig.Containers, "azure-kms-provider-backup")
		g.Expect(backup).To(BeNil(), "expected no backup container when backup key is not specified")
	})
}

func TestGenerateKMSEncryptionConfig_Azure(t *testing.T) {
	tests := []struct {
		name   string
		spec   *hyperv1.AzureKMSSpec
		checks func(g Gomega, encConfig interface{})
	}{
		{
			name: "When only active key is configured it should create encryption config with one KMS provider and Identity fallback",
			spec: validAzureKMSSpec(),
		},
		{
			name: "When backup key is also configured it should create encryption config with two KMS providers and Identity fallback",
			spec: validAzureKMSSpecWithBackup(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			provider, err := newTestAzureKMSProvider(tc.spec, "test-kms-image:latest", AzureKMSProviderOptions{
				IsSelfManaged: false,
			})
			g.Expect(err).NotTo(HaveOccurred())

			encConfig, err := provider.GenerateKMSEncryptionConfig("v2")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(encConfig).NotTo(BeNil())

			g.Expect(encConfig.Kind).To(Equal(encryptionConfigurationKind))
			g.Expect(encConfig.Resources).To(HaveLen(1))
			g.Expect(encConfig.Resources[0].Resources).To(Equal(config.KMSEncryptedObjects()))

			providers := encConfig.Resources[0].Providers
			if tc.spec.BackupKey != nil { //nolint:staticcheck
				g.Expect(providers).To(HaveLen(3), "expected active KMS + backup KMS + Identity")
			} else {
				g.Expect(providers).To(HaveLen(2), "expected active KMS + Identity")
			}

			// First provider is always the active KMS
			g.Expect(providers[0].KMS).NotTo(BeNil())
			activeKeyHash, err := util.HashStruct(tc.spec.ActiveKey)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(providers[0].KMS.Name).To(Equal(fmt.Sprintf("azure-%s", activeKeyHash)))
			g.Expect(providers[0].KMS.APIVersion).To(Equal("v2"))
			g.Expect(providers[0].KMS.Endpoint).To(Equal(azureActiveKMSUnixSocket))
			g.Expect(providers[0].KMS.Timeout).To(Equal(&metav1.Duration{Duration: 35 * time.Second}))

			if tc.spec.BackupKey != nil { //nolint:staticcheck
				g.Expect(providers[1].KMS).NotTo(BeNil())
				backupKeyHash, err := util.HashStruct(tc.spec.BackupKey) //nolint:staticcheck
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(providers[1].KMS.Name).To(Equal(fmt.Sprintf("azure-%s", backupKeyHash)))
				g.Expect(providers[1].KMS.Endpoint).To(Equal(azureBackupKMSUnixSocket))
				g.Expect(providers[1].KMS.Timeout).To(Equal(&metav1.Duration{Duration: 35 * time.Second}))
				// Last provider is Identity
				g.Expect(providers[2].Identity).NotTo(BeNil())
			} else {
				// Last provider is Identity
				g.Expect(providers[1].Identity).NotTo(BeNil())
			}
		})
	}
}

func TestAdaptAzureSecretProvider(t *testing.T) {
	tests := []struct {
		name        string
		credSecret  string
		expectError bool
		errContains string
	}{
		{
			name:        "When managed identity credentials secret name is empty it should return an error",
			credSecret:  "",
			expectError: true,
			errContains: "managed identity credentials secret name is required",
		},
		{
			name:        "When managed identity credentials secret name is set it should succeed",
			credSecret:  "kms-identity-creds",
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					SecretEncryption: &hyperv1.SecretEncryptionSpec{
						KMS: &hyperv1.KMSSpec{
							Provider: hyperv1.AZURE,
							Azure: &hyperv1.AzureKMSSpec{
								ActiveKey: hyperv1.AzureKMSKey{
									KeyVaultName: "test-vault",
									KeyName:      "test-key",
									KeyVersion:   "1",
								},
								KMS: hyperv1.ManagedIdentity{
									CredentialsSecretName: tc.credSecret,
								},
							},
						},
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
								ManagedIdentities: &hyperv1.AzureResourceManagedIdentities{
									ControlPlane: hyperv1.ControlPlaneManagedIdentities{
										ManagedIdentitiesKeyVault: hyperv1.ManagedAzureKeyVault{
											Name:     "test-kv",
											TenantID: "test-tenant-id",
										},
									},
								},
							},
						},
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}
			secretProvider := &secretsstorev1.SecretProviderClass{}

			err := AdaptAzureSecretProvider(cpContext, secretProvider)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.errContains))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
