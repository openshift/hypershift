package mcs

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"

	configv1 "github.com/openshift/api/config/v1"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewMCSParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		hcp    *hyperv1.HostedControlPlane
		verify func(g Gomega, params *MCSParams, err error)
	}{
		{
			name: "When HCP has no configuration, it should return empty APIServer spec",
			hcp:  &hyperv1.HostedControlPlane{},
			verify: func(g Gomega, params *MCSParams, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(params.APIServer).ToNot(BeNil())
				g.Expect(params.APIServer.Spec).To(Equal(configv1.APIServerSpec{}))
			},
		},
		{
			name: "When HCP has APIServer configuration, it should propagate the spec",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							Audit: configv1.Audit{
								Profile: configv1.WriteRequestBodiesAuditProfileType,
							},
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileIntermediateType,
							},
						},
					},
				},
			},
			verify: func(g Gomega, params *MCSParams, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(params.APIServer).ToNot(BeNil())
				g.Expect(params.APIServer.Spec.Audit.Profile).To(Equal(configv1.WriteRequestBodiesAuditProfileType))
				g.Expect(params.APIServer.Spec.TLSSecurityProfile).ToNot(BeNil())
				g.Expect(params.APIServer.Spec.TLSSecurityProfile.Type).To(Equal(configv1.TLSProfileIntermediateType))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			params, err := NewMCSParams(tt.hcp, &corev1.Secret{}, &corev1.Secret{}, &corev1.ConfigMap{}, &corev1.ConfigMap{})
			tt.verify(g, params, err)
		})
	}
}

func TestReconcileMachineConfigServerConfig(t *testing.T) {
	t.Parallel()

	baseParams := func() *MCSParams {
		return &MCSParams{
			OwnerRef:        config.OwnerRef{},
			RootCA:          &corev1.Secret{},
			KubeletClientCA: &corev1.ConfigMap{},
			PullSecret:      &corev1.Secret{},
			DNS:             &configv1.DNS{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
			Infrastructure:  &configv1.Infrastructure{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
			Network:         &configv1.Network{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
			Proxy:           &configv1.Proxy{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
			Image:           &configv1.Image{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
			APIServer:       &configv1.APIServer{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
			InstallConfig:   &globalconfig.InstallConfig{},
		}
	}

	deserializeAPIServer := func(g Gomega, data string) *configv1.APIServer {
		obj, _, err := api.YamlSerializer.Decode([]byte(data), nil, &configv1.APIServer{})
		g.Expect(err).ToNot(HaveOccurred())
		apiServer, ok := obj.(*configv1.APIServer)
		g.Expect(ok).To(BeTrue())
		return apiServer
	}

	tests := []struct {
		name   string
		params *MCSParams
		verify func(g Gomega, cm *corev1.ConfigMap)
	}{
		{
			name:   "When APIServer has no spec, it should set an empty apiserver config",
			params: baseParams(),
			verify: func(g Gomega, cm *corev1.ConfigMap) {
				g.Expect(cm.Data).To(HaveKey("cluster-apiserver-config.yaml"))
				apiServer := deserializeAPIServer(g, cm.Data["cluster-apiserver-config.yaml"])
				g.Expect(apiServer.Spec).To(Equal(configv1.APIServerSpec{}))
			},
		},
		{
			name: "When APIServer has audit profile, it should serialize it into the configmap",
			params: func() *MCSParams {
				p := baseParams()
				p.APIServer.Spec.Audit.Profile = configv1.WriteRequestBodiesAuditProfileType
				return p
			}(),
			verify: func(g Gomega, cm *corev1.ConfigMap) {
				g.Expect(cm.Data).To(HaveKey("cluster-apiserver-config.yaml"))
				apiServer := deserializeAPIServer(g, cm.Data["cluster-apiserver-config.yaml"])
				g.Expect(apiServer.Spec.Audit.Profile).To(Equal(configv1.WriteRequestBodiesAuditProfileType))
			},
		},
		{
			name: "When APIServer has TLS and named certs, it should serialize them into the configmap",
			params: func() *MCSParams {
				p := baseParams()
				p.APIServer.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{
					Type: configv1.TLSProfileIntermediateType,
				}
				p.APIServer.Spec.ServingCerts = configv1.APIServerServingCerts{
					NamedCertificates: []configv1.APIServerNamedServingCert{
						{
							Names:              []string{"api.example.com"},
							ServingCertificate: configv1.SecretNameReference{Name: "api-cert"},
						},
					},
				}
				return p
			}(),
			verify: func(g Gomega, cm *corev1.ConfigMap) {
				g.Expect(cm.Data).To(HaveKey("cluster-apiserver-config.yaml"))
				apiServer := deserializeAPIServer(g, cm.Data["cluster-apiserver-config.yaml"])
				g.Expect(apiServer.Spec.TLSSecurityProfile).ToNot(BeNil())
				g.Expect(apiServer.Spec.TLSSecurityProfile.Type).To(Equal(configv1.TLSProfileIntermediateType))
				g.Expect(apiServer.Spec.ServingCerts.NamedCertificates).To(HaveLen(1))
				g.Expect(apiServer.Spec.ServingCerts.NamedCertificates[0].Names).To(ContainElement("api.example.com"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			cm := &corev1.ConfigMap{}
			err := ReconcileMachineConfigServerConfig(cm, tt.params)
			g.Expect(err).ToNot(HaveOccurred())
			tt.verify(g, cm)
		})
	}
}

func TestMasterConfigPool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		verify func(g Gomega, pool *mcfgv1.MachineConfigPool)
	}{
		{
			name: "When called, it should return a pool named master",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Name).To(Equal("master"))
			},
		},
		{
			name: "When called, it should have the mco-built-in label",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Labels).To(HaveKeyWithValue("machineconfiguration.openshift.io/mco-built-in", ""))
			},
		},
		{
			name: "When called, it should select worker machine configs",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Spec.MachineConfigSelector).NotTo(BeNil())
				g.Expect(pool.Spec.MachineConfigSelector.MatchLabels).To(HaveKeyWithValue("machineconfiguration.openshift.io/role", "worker"))
			},
		},
		{
			name: "When called, it should select worker nodes",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Spec.NodeSelector).NotTo(BeNil())
				g.Expect(pool.Spec.NodeSelector.MatchLabels).To(HaveKeyWithValue("node-role.kubernetes.io/worker", ""))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			pool := masterConfigPool()
			tt.verify(g, pool)
		})
	}
}

func TestWorkerConfigPool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		verify func(g Gomega, pool *mcfgv1.MachineConfigPool)
	}{
		{
			name: "When called, it should return a pool named worker",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Name).To(Equal("worker"))
			},
		},
		{
			name: "When called, it should have the mco-built-in label",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Labels).To(HaveKeyWithValue("machineconfiguration.openshift.io/mco-built-in", ""))
			},
		},
		{
			name: "When called, it should select worker machine configs",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Spec.MachineConfigSelector).NotTo(BeNil())
				g.Expect(pool.Spec.MachineConfigSelector.MatchLabels).To(HaveKeyWithValue("machineconfiguration.openshift.io/role", "worker"))
			},
		},
		{
			name: "When called, it should select worker nodes",
			verify: func(g Gomega, pool *mcfgv1.MachineConfigPool) {
				g.Expect(pool.Spec.NodeSelector).NotTo(BeNil())
				g.Expect(pool.Spec.NodeSelector.MatchLabels).To(HaveKeyWithValue("node-role.kubernetes.io/worker", ""))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			pool := workerConfigPool()
			tt.verify(g, pool)
		})
	}
}
