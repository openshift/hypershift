package kubevirt

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	clientcmdlatest "k8s.io/client-go/tools/clientcmd/api/latest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func createServiceAccount(ctx context.Context, hcp *hyperv1.HostedControlPlane, c client.Client, createOrUpdate upsert.CreateOrUpdateFN) (*corev1.ServiceAccount, error) {
	ownerRef := config.OwnerRefFrom(hcp)
	sa := ccmServiceAccount(hcp.Namespace)
	if _, err := createOrUpdate(ctx, c, sa, func() error {
		return reconcileCCMServiceAccount(sa, ownerRef)
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile Kubevirt cloud provider service account: %w", err)
	}
	role := ccmRole(hcp.Namespace)
	if _, err := createOrUpdate(ctx, c, role, func() error {
		return reconcileCCMRole(role, ownerRef)
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile Kubevirt cloud provider role: %w", err)
	}
	roleBinding := ccmRoleBinding(hcp.Namespace)
	if _, err := createOrUpdate(ctx, c, roleBinding, func() error {
		return reconcileCCMRoleBinding(roleBinding, ownerRef, sa, role)
	}); err != nil {
		return nil, fmt.Errorf("failed to reconcile Kubevirt cloud provider rolebinding: %w", err)
	}

	// clusterRole := ccmClusterRole()
	// if _, err := r.CreateOrUpdate(ctx, r, clusterRole, func() error {
	// 	return kubevirt.reconcileCCMClusterRole(clusterRole)
	// }); err != nil {
	// 	return nil, fmt.Errorf("failed to reconcile Kubevirt cloud provider cluster role: %w", err)
	// }
	// clusterRoleBinding := ccmClusterRoleBinding()
	// if _, err := r.CreateOrUpdate(ctx, r, clusterRoleBinding, func() error {
	// 	return kubevirt.reconcileCCMClusterRoleBinding(clusterRoleBinding, sa, clusterRole)
	// }); err != nil {
	// 	return nil, fmt.Errorf("failed to reconcile Kubevirt cloud provider cluster rolebinding: %w", err)
	// }
	return sa, nil
}

func CreateInClusterConfig(ctx context.Context, hcp *hyperv1.HostedControlPlane, c client.Client, createOrUpdate upsert.CreateOrUpdateFN) ([]byte, error) {
	sa, err := createServiceAccount(ctx, hcp, c, createOrUpdate)
	if err != nil {
		return nil, err
	}

	token, caCert, err := getTokenData(ctx, c, sa)
	if err != nil {
		return nil, err
	}

	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clusters := make(map[string]*clientcmdapi.Cluster)
	clusters["infra-cluster"] = &clientcmdapi.Cluster{
		Server:                   restConfig.Host,
		CertificateAuthorityData: caCert,
	}

	contexts := make(map[string]*clientcmdapi.Context)
	contexts["default-context"] = &clientcmdapi.Context{
		Cluster:   "infra-cluster",
		Namespace: sa.Namespace,
		AuthInfo:  sa.Namespace,
	}

	authinfos := make(map[string]*clientcmdapi.AuthInfo)
	authinfos[sa.Namespace] = &clientcmdapi.AuthInfo{
		Token: string(token),
	}

	config := &clientcmdapi.Config{
		Kind:           "Config",
		APIVersion:     "v1",
		Clusters:       clusters,
		Contexts:       contexts,
		CurrentContext: "default-context",
		AuthInfos:      authinfos,
	}

	json, err := runtime.Encode(clientcmdlatest.Codec, config)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the configuration: %w", err)
	}
	output, err := yaml.JSONToYAML(json)
	if err != nil {
		return nil, fmt.Errorf("failed to create yaml configuration: %w", err)
	}

	return output, nil
}

func getTokenData(ctx context.Context, c client.Client, sa *corev1.ServiceAccount) ([]byte, []byte, error) {
	requestedSecretName := sa.Name + "-token"
	secretList := &corev1.SecretList{}
	opts := []client.ListOption{
		client.InNamespace(sa.Namespace),
	}
	if err := c.List(ctx, secretList, opts...); err != nil {
		return nil, nil, fmt.Errorf("can't get the token secret list in namespace %s, %w", sa.Namespace, err)
	}
	for _, secret := range secretList.Items {
		if strings.HasPrefix(secret.Name, requestedSecretName) {
			token, ok := secret.Data["token"]
			if !ok {
				return nil, nil, fmt.Errorf("can't find the koken in the secret")
			}
			caCert, ok := secret.Data["ca.crt"]
			if !ok {
				return nil, nil, fmt.Errorf("can't find the ca.crt in the secret")
			}
			return token, caCert, nil
		}
	}

	return nil, nil, fmt.Errorf("can't find secret %s", requestedSecretName)
}
