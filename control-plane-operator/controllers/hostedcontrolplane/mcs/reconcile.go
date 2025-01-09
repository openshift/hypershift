package mcs

import (
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/util"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ReconcileMachineConfigServerConfig(cm *corev1.ConfigMap, p *MCSParams) error {
	p.OwnerRef.ApplyTo(cm)
	cm.Data = map[string]string{}
	serializedDNS, err := serialize(p.DNS)
	if err != nil {
		return err
	}
	serializedInfra, err := serialize(p.Infrastructure)
	if err != nil {
		return err
	}
	serializedNetwork, err := serialize(p.Network)
	if err != nil {
		return err
	}
	serializedProxy, err := serialize(p.Proxy)
	if err != nil {
		return err
	}
	serializedImage, err := serialize(p.Image)
	if err != nil {
		return err
	}
	serializedMasterConfigPool, err := serializeConfigPool(masterConfigPool())
	if err != nil {
		return err
	}
	serializedWorkerConfigPool, err := serializeConfigPool(workerConfigPool())
	if err != nil {
		return err
	}

	if p.UserCA != nil && len(p.UserCA.Data) > 0 {
		serializedUserCA, err := serialize(p.UserCA)
		if err != nil {
			return err
		}
		cm.Data["user-ca-bundle-config.yaml"] = serializedUserCA
	}

	cm.Data["root-ca.crt"] = string(p.RootCA.Data[certs.CASignerCertMapKey])
	cm.Data["signer-ca.crt"] = p.KubeletClientCA.Data[certs.CASignerCertMapKey]
	cm.Data["cluster-dns-02-config.yaml"] = serializedDNS
	cm.Data["cluster-infrastructure-02-config.yaml"] = serializedInfra
	cm.Data["cluster-network-02-config.yaml"] = serializedNetwork
	cm.Data["cluster-proxy-01-config.yaml"] = serializedProxy
	cm.Data["image-config.yaml"] = serializedImage
	cm.Data["install-config.yaml"] = p.InstallConfig.String()
	cm.Data["master.machineconfigpool.yaml"] = serializedMasterConfigPool
	cm.Data["worker.machineconfigpool.yaml"] = serializedWorkerConfigPool
	cm.Data["configuration-hash"] = p.ConfigurationHash
	return nil
}

func serialize(obj client.Object) (string, error) {
	return util.SerializeResource(obj, api.Scheme)
}

var (
	machineConfigPoolScheme = runtime.NewScheme()
)

func init() {
	mcfgv1.AddToScheme(machineConfigPoolScheme)
}

func serializeConfigPool(obj client.Object) (string, error) {
	return util.SerializeResource(obj, machineConfigPoolScheme)
}

func masterConfigPool() *mcfgv1.MachineConfigPool {
	return &mcfgv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			// NOTE: this configpool is master in name only but is used to
			// render config for workers in a hypershift cluster.
			// TODO: modify MCS so that it allows a named config pool to be rendered
			// in bootstrap mode.
			Name: "master",
			Labels: map[string]string{
				"machineconfiguration.openshift.io/mco-built-in": "",
			},
		},
		Spec: mcfgv1.MachineConfigPoolSpec{
			MachineConfigSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					// NOTE: the master config pool is the only pool that
					// can be served by the MCS in bootstrap mode. For the
					// hosted cluster use case, all we want is the worker
					// config, therefore the selector is for worker configs.
					"machineconfiguration.openshift.io/role": "worker",
				},
			},
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					// Use the worker node selector, since we want this
					// configpool to apply to all workers in the cluster.
					"node-role.kubernetes.io/worker": "",
				},
			},
		},
	}
}

func workerConfigPool() *mcfgv1.MachineConfigPool {
	// NOTE: This configpool is here because we need to provide one to the
	// MCS in bootstrap mode, however it is not used in rendering configuration.
	return &mcfgv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "worker",
			Labels: map[string]string{
				"machineconfiguration.openshift.io/mco-built-in": "",
			},
		},
		Spec: mcfgv1.MachineConfigPoolSpec{
			MachineConfigSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"machineconfiguration.openshift.io/role": "worker",
				},
			},
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"node-role.kubernetes.io/worker": "",
				},
			},
		},
	}
}
