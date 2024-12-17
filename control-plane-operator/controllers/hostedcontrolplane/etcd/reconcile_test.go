package etcd

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

const (
	etcdScriptTemplate = `
CLUSTER_STATE="new"
if [[ -f /etc/etcd/clusterstate/existing ]]; then
  CLUSTER_STATE="existing"
fi

/usr/bin/etcd \
--data-dir=/var/lib/data \
--name=${HOSTNAME} \
--initial-advertise-peer-urls=https://${HOSTNAME}.etcd-discovery.${NAMESPACE}.svc:2380 \
--listen-peer-urls=https://%s:2380 \
--listen-client-urls=https://%s:2379,https://localhost:2379 \
--advertise-client-urls=https://${HOSTNAME}.etcd-discovery.${NAMESPACE}.svc:2379 \
--listen-metrics-urls=https://%s:2382 \
--initial-cluster-token=etcd-cluster \
--initial-cluster=${INITIAL_CLUSTER} \
--initial-cluster-state=${CLUSTER_STATE} \
--quota-backend-bytes=${QUOTA_BACKEND_BYTES} \
--snapshot-count=10000 \
--peer-client-cert-auth=true \
--peer-cert-file=/etc/etcd/tls/peer/peer.crt \
--peer-key-file=/etc/etcd/tls/peer/peer.key \
--peer-trusted-ca-file=/etc/etcd/tls/etcd-ca/ca.crt \
--client-cert-auth=true \
--cert-file=/etc/etcd/tls/server/server.crt \
--key-file=/etc/etcd/tls/server/server.key \
--trusted-ca-file=/etc/etcd/tls/etcd-ca/ca.crt
`
	etcdMetricsScriptTemplate = `
etcd grpc-proxy start \
--endpoints https://localhost:2382 \
--metrics-addr https://%s:2381 \
--listen-addr %s:2383 \
--advertise-client-url ""  \
--key /etc/etcd/tls/peer/peer.key \
--key-file /etc/etcd/tls/server/server.key \
--cert /etc/etcd/tls/peer/peer.crt \
--cert-file /etc/etcd/tls/server/server.crt \
--cacert /etc/etcd/tls/etcd-ca/ca.crt \
--trusted-ca-file /etc/etcd/tls/etcd-metrics-ca/ca.crt
`

	podIPIpv6         = "[${POD_IP}]"
	allInterfacesIpv6 = "[::]"
	loInterfaceIpv6   = "[::1]"

	podIPIpv4         = "${POD_IP}"
	allInterfacesIpv4 = "0.0.0.0"
	loInterfaceIpv4   = "127.0.0.1"
)

func TestBuildEtcdInitContainer(t *testing.T) {
	tests := []struct {
		name        string
		params      EtcdParams
		expectedEnv []corev1.EnvVar
	}{
		{
			name: "single replica container env as expected",
			params: EtcdParams{
				EtcdImage: "animage",
				DeploymentConfig: config.DeploymentConfig{
					Replicas: 1,
				},
				StorageSpec: hyperv1.ManagedEtcdStorageSpec{
					RestoreSnapshotURL: []string{"arestoreurl"},
				},
			},
			expectedEnv: []corev1.EnvVar{
				{
					Name:  "RESTORE_URL_ETCD",
					Value: "arestoreurl",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			c := corev1.Container{}
			buildEtcdInitContainer(&tt.params)(&c)
			g.Expect(c.Env).Should(ConsistOf(tt.expectedEnv))
		})
	}
}

func TestBuildEtcdContainer(t *testing.T) {
	tests := []struct {
		name            string
		namespace       string
		params          EtcdParams
		expectedCommand []string
	}{
		{
			name:      "given ipv4 environment, it should return a single replica container with ipv4 script",
			namespace: "test-ns",
			params: EtcdParams{
				EtcdImage: "animage",
				DeploymentConfig: config.DeploymentConfig{
					Replicas: 1,
				},
				StorageSpec: hyperv1.ManagedEtcdStorageSpec{
					RestoreSnapshotURL: []string{"arestoreurl"},
				},
				IPv6: false,
			},
			expectedCommand: []string{"/bin/sh", "-c", fmt.Sprintf(etcdScriptTemplate, podIPIpv4, podIPIpv4, allInterfacesIpv4)},
		},
		{
			name:      "given ipv6 environment, it should return a three replica container with ipv6 script",
			namespace: "test-ns",
			params: EtcdParams{
				EtcdImage: "animage",
				DeploymentConfig: config.DeploymentConfig{
					Replicas: 1,
				},
				StorageSpec: hyperv1.ManagedEtcdStorageSpec{
					RestoreSnapshotURL: []string{"arestoreurl"},
				},
				IPv6: true,
			},
			expectedCommand: []string{"/bin/sh", "-c", fmt.Sprintf(etcdScriptTemplate, podIPIpv6, podIPIpv6, allInterfacesIpv6)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			c := corev1.Container{}
			buildEtcdContainer(&tt.params, tt.namespace)(&c)
			g.Expect(c.Command).Should(BeEquivalentTo(tt.expectedCommand))
		})
	}
}

func TestBuildEtcdMetricsContainer(t *testing.T) {
	tests := []struct {
		name            string
		namespace       string
		params          EtcdParams
		expectedCommand []string
	}{
		{
			name:      "given ipv4 environment, it should return a single replica container with ipv4 script",
			namespace: "test-ns",
			params: EtcdParams{
				EtcdImage: "animage",
				DeploymentConfig: config.DeploymentConfig{
					Replicas: 1,
				},
				StorageSpec: hyperv1.ManagedEtcdStorageSpec{
					RestoreSnapshotURL: []string{"arestoreurl"},
				},
				IPv6: false,
			},
			expectedCommand: []string{"/bin/sh", "-c", fmt.Sprintf(etcdMetricsScriptTemplate, allInterfacesIpv4, loInterfaceIpv4)},
		},
		{
			name:      "given ipv6 environment, it should return a three replica container with ipv6 script",
			namespace: "test-ns",
			params: EtcdParams{
				EtcdImage: "animage",
				DeploymentConfig: config.DeploymentConfig{
					Replicas: 1,
				},
				StorageSpec: hyperv1.ManagedEtcdStorageSpec{
					RestoreSnapshotURL: []string{"arestoreurl"},
				},
				IPv6: true,
			},
			expectedCommand: []string{"/bin/sh", "-c", fmt.Sprintf(etcdMetricsScriptTemplate, allInterfacesIpv6, loInterfaceIpv6)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			c := corev1.Container{}
			buildEtcdMetricsContainer(&tt.params, tt.namespace)(&c)
			g.Expect(c.Command).Should(BeEquivalentTo(tt.expectedCommand))
		})
	}
}
