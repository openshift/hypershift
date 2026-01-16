package extend

import (
	"context"
	"fmt"
	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"math/rand"
	"os"
	"os/exec"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"
)

const (
	requiredHCConditions = "ValidHostedControlPlaneConfiguration True ClusterVersionSucceeding True " +
		"Degraded False EtcdAvailable True KubeAPIServerAvailable True InfrastructureReady True " +
		"Available True ValidConfiguration True SupportedHostedCluster True " +
		"IgnitionEndpointAvailable True ReconciliationActive True ValidReleaseImage True ReconciliationSucceeded True"
)

type HostedClusterConfig struct {
	Name       string
	Namespace  string
	Kubeconfig string
	Platform   string
}

func ValidHypershiftAndGetGuestKubeConf(ctx context.Context, client crclient.Client) (*HostedClusterConfig, error) {
	/* TODO: migrate for ROSA
		if IsROSA() {
		  e2e.Logf("there is a ROSA env")
		  hostedClusterName, hostedclusterKubeconfig, hostedClusterNs := ROSAValidHypershiftAndGetGuestKubeConf(oc)
	    }
	}*/
	logger := ctrl.LoggerFrom(ctx)
	operatorNS, err := GetHyperShiftOperatorNamespace(ctx, client)
	if err != nil {
		return nil, err
	}
	if len(operatorNS) <= 0 {
		return nil, fmt.Errorf("there is no hypershift operator on host cluster")
	}

	hostedclusterNS, err := GetHostedClusterNamespace(ctx, client)
	if err != nil {
		return nil, err
	}
	if len(hostedclusterNS) <= 0 {
		return nil, fmt.Errorf("there is no hosted cluster NS in mgmt cluster")
	}

	hcList := &hyperv1.HostedClusterList{}
	if err := client.List(ctx, hcList, &crclient.ListOptions{Namespace: hostedclusterNS}); err != nil {
		return nil, err
	}
	if len(hcList.Items) == 0 {
		return nil, fmt.Errorf("no hosted clusters found")
	}

	var clusterNames []string
	for _, hc := range hcList.Items {
		clusterNames = append(clusterNames, hc.Name)
	}

	podList := &corev1.PodList{}
	err = client.List(ctx, podList, &crclient.ListOptions{Namespace: operatorNS},
		crclient.MatchingLabelsSelector{Selector: getOperatorSelector()},
	)
	if err != nil {
		logger.Error(err, "failed to list hypershift operator pods")
		return nil, err
	}

	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("no operator pods found")
	}

	for _, pod := range podList.Items {
		if pod.Status.Phase != corev1.PodRunning {
			return nil, fmt.Errorf("hypershift operator pod is not in running state")
		}
	}
	clusterName := clusterNames[0]
	hostedClusterKubeconfigFile, err := getOrCreateKubeconfig(ctx, clusterName, hostedclusterNS)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	platform, err := GetHostedClusterPlatform(ctx, client, hostedclusterNS, clusterName)
	if err != nil {
		return nil, err
	}
	return &HostedClusterConfig{
		Name:       clusterName,
		Namespace:  hostedclusterNS,
		Kubeconfig: hostedClusterKubeconfigFile,
		Platform:   platform,
	}, nil
}

func CheckHCConditions(ctx context.Context, c crclient.Client, hcnamespace, hcname string) (bool, error) {
	hc := &hyperv1.HostedCluster{}
	key := types.NamespacedName{
		Name:      hcname,
		Namespace: hcnamespace,
	}

	if err := c.Get(ctx, key, hc); err != nil {
		return false, fmt.Errorf("failed to get hosted cluster: %w", err)
	}

	var strbuilder strings.Builder
	for _, cond := range hc.Status.Conditions {
		fmt.Fprintf(&strbuilder, "%s %s ", cond.Type, cond.Status)
	}

	currentConditions := strbuilder.String()
	expectedConditions := strings.Fields(requiredHCConditions)

	return checkStatusConditions(currentConditions, expectedConditions), nil
}

func checkStatusConditions(src string, expect []string) bool {
	if expect == nil || len(expect) <= 0 {
		return true
	}
	for i := 0; i < len(expect); i++ {
		if !strings.Contains(src, expect[i]) {
			return false
		}
	}
	return true
}

func GetHostedClusterVersion(ctx context.Context, guestClient crclient.Client, hostedClusterNs, hostedClusterName string) (semver.Version, error) {
	cv := &configv1.ClusterVersion{}
	err := guestClient.Get(ctx, crclient.ObjectKey{Name: "version"}, cv)
	if err != nil {
		return semver.Version{}, fmt.Errorf("failed to get cluster version: %w", err)
	}
	var versionStr string
	for _, h := range cv.Status.History {
		if h.State == configv1.CompletedUpdate {
			versionStr = h.Version
			break
		}
	}
	if versionStr == "" {
		return semver.Version{}, fmt.Errorf("no completed update found in cluster version history")
	}

	hcVersion, err := semver.Parse(versionStr)
	if err != nil {
		return semver.Version{}, fmt.Errorf("failed to parse cluster version: %w", err)
	}
	return hcVersion, nil
}

func GetHostedClusterNamespace(ctx context.Context, client crclient.Client) (string, error) {
	hcList := &hyperv1.HostedClusterList{}
	err := client.List(ctx, hcList)
	if err != nil {
		return "", fmt.Errorf("failed to list hosted clusters: %w", err)
	}
	if len(hcList.Items) == 0 {
		return "", nil
	}
	return hcList.Items[0].Namespace, nil
}

func GetHostedClusterPlatform(ctx context.Context, client crclient.Client, namespace, name string) (string, error) {
	hc := &hyperv1.HostedCluster{}
	err := client.Get(
		ctx,
		crclient.ObjectKey{
			Namespace: namespace,
			Name:      name,
		},
		hc,
	)
	if err != nil {
		return "", fmt.Errorf("failed to get hosted cluster platform: %w", err)
	}
	return string(hc.Spec.Platform.Type), nil
}

func GetRandomString() string {
	chars := "abcdefghijklmnopqrstuvwxyz0123456789"
	seed := rand.New(rand.NewSource(time.Now().UnixNano()))
	buffer := make([]byte, 8)
	for index := range buffer {
		buffer[index] = chars[seed.Intn(len(chars))]
	}
	return string(buffer)
}

func getOperatorSelector() labels.Selector {
	return labels.SelectorFromSet(labels.Set{
		"hypershift.openshift.io/operator-component": "operator",
		"app": "operator",
	})
}

func getOrCreateKubeconfig(ctx context.Context, clusterName, hostedclusterNS string) (string, error) {
	logger := ctrl.LoggerFrom(ctx)

	if envKubeconfig := os.Getenv("GUEST_KUBECONFIG"); envKubeconfig != "" {
		logger.Info("Using configured guest kubeconfig", "kubeconfig", envKubeconfig)
		return envKubeconfig, nil
	}

	kubeconfigFile := "/tmp/guestcluster-kubeconfig-" + clusterName + "-" + GetRandomString()
	_, err := exec.Command("bash", "-c", fmt.Sprintf("hypershift create kubeconfig --name %s --namespace %s > %s",
		clusterName, hostedclusterNS, kubeconfigFile)).Output()
	if err != nil {
		logger.Error(err, "failed to create kubeconfig file")
		return "", fmt.Errorf("failed to create kubeconfig: %w", err)
	}
	logger.Info("Created hosted cluster kubeconfig", "kubeconfig", kubeconfigFile)
	return kubeconfigFile, nil
}
