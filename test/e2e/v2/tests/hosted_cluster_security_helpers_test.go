//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	kasconst "github.com/openshift/hypershift/pkg/kas"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	utilexec "k8s.io/client-go/util/exec"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func securityAdmissionVersion(tc *internal.TestContext, hostedCluster *hyperv1.HostedCluster) (semver.Version, error) {
	if hostedCluster.Status.Version == nil || hostedCluster.Status.Version.Desired.Version == "" {
		return healthReleaseVersion(tc)
	}
	return semver.Parse(hostedCluster.Status.Version.Desired.Version)
}

func securityAdmissionAtLeast(tc *internal.TestContext, hostedCluster *hyperv1.HostedCluster, minimum semver.Version) (bool, error) {
	version, err := securityAdmissionVersion(tc, hostedCluster)
	return version.GTE(minimum), err
}

func securityRequiredAdmissionPolicies(tc *internal.TestContext) ([]string, error) {
	version, err := healthReleaseVersion(tc)
	if err != nil {
		return nil, err
	}
	policies := []string{kasconst.AdmissionPolicyNameConfig, kasconst.AdmissionPolicyNameMirror, kasconst.AdmissionPolicyNameICSP, kasconst.AdmissionPolicyNameInfra, kasconst.AdmissionPolicyNameNTOMirroredConfigs}
	if version.GTE(semver.MustParse("5.1.0")) {
		policies = append(policies, kasconst.AdmissionPolicyNameRBAC)
	}
	return policies, nil
}

func securityAdmissionImpersonation(config *rest.Config) *rest.Config {
	config = rest.CopyConfig(config)
	config.Impersonate = rest.ImpersonationConfig{UserName: "hypershift-e2e-rbac-vap-test", Groups: []string{"system:masters"}}
	return config
}

func securityAdmissionRejection(err error) error {
	if err == nil {
		return fmt.Errorf("expected ValidatingAdmissionPolicy rejection, request succeeded")
	}
	if !strings.Contains(err.Error(), "ValidatingAdmissionPolicy") {
		return fmt.Errorf("expected ValidatingAdmissionPolicy rejection: %w", err)
	}
	return nil
}

func securityDeniedAuditChange(ctx context.Context, client crclient.Client, apiServer *configv1.APIServer) error {
	updated := apiServer.DeepCopy()
	if updated.Spec.Audit.Profile == configv1.AllRequestBodiesAuditProfileType {
		updated.Spec.Audit.Profile = configv1.DefaultAuditProfileType
	} else {
		updated.Spec.Audit.Profile = configv1.AllRequestBodiesAuditProfileType
	}
	return securityAdmissionRejection(client.Update(ctx, updated, crclient.DryRunAll))
}

func securityDeniedBindingDeletions(ctx context.Context, client crclient.Client) error {
	for _, name := range []string{"hcco-cluster-admin", "kas-bootstrap-container-cluster-admin"} {
		binding := &rbacv1.ClusterRoleBinding{}
		if err := client.Get(ctx, crclient.ObjectKey{Name: name}, binding); err != nil {
			return fmt.Errorf("get protected ClusterRoleBinding %s: %w", name, err)
		}
		if err := securityAdmissionRejection(client.Delete(ctx, binding, crclient.DryRunAll)); err != nil {
			return fmt.Errorf("delete protected ClusterRoleBinding %s: %w", name, err)
		}
	}
	return nil
}

func securitySetNetworkMTU(ctx context.Context, client crclient.Client, mtu int) error {
	network := &configv1.Network{}
	if err := client.Get(ctx, crclient.ObjectKey{Name: "cluster"}, network); err != nil {
		return err
	}
	network.Status.ClusterNetworkMTU = mtu
	return client.Update(ctx, network)
}

func securitySetOperatorHubSources(ctx context.Context, client crclient.Client, disabled bool) error {
	operatorHub := &configv1.OperatorHub{}
	if err := client.Get(ctx, crclient.ObjectKey{Name: "cluster"}, operatorHub); err != nil {
		return err
	}
	operatorHub.Spec.DisableAllDefaultSources = disabled
	return client.Update(ctx, operatorHub)
}

func securityManagementKASComponents() sets.Set[string] {
	return sets.New("cluster-network-operator", "endpoint-resolver", "ignition-server", "cluster-storage-operator", "csi-snapshot-controller-operator", "machine-approver", "cluster-autoscaler", "cluster-node-tuning-operator", "capi-provider-controller-manager", "capi-provider", "cluster-api", "etcd", "control-plane-operator", "control-plane-pki-operator", "hosted-cluster-config-operator", "cloud-controller-manager", "olm-collect-profiles", "aws-ebs-csi-driver-operator", "karpenter", "karpenter-operator", "featuregate-generator")
}

func securityPodComponent(pod corev1.Pod) string {
	if pod.Labels["app"] != "" {
		return pod.Labels["app"]
	}
	if pod.Labels["name"] != "" {
		return pod.Labels["name"]
	}
	if strings.HasPrefix(pod.Labels["job-name"], "olm-collect-profiles") {
		return "olm-collect-profiles"
	}
	if pod.Labels["job-name"] == "featuregate-generator" {
		return "featuregate-generator"
	}
	return ""
}

func securityCurlConnectionDenied(err error) bool {
	var exitError utilexec.ExitError
	return errors.As(err, &exitError) && exitError.Exited() && (exitError.ExitStatus() == 7 || exitError.ExitStatus() == 28)
}
