//go:build e2ev2

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func metricsNodeTuningApplicable(tc *internal.TestContext, hostedCluster *hyperv1.HostedCluster) (bool, error) {
	if err := tc.Context.Err(); err != nil {
		return false, err
	}
	var version semver.Version
	var err error
	if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		version, err = healthReleaseVersion(tc)
	} else {
		version, err = tc.GetHostedClusterVersion()
	}
	if err != nil {
		return false, err
	}
	if version.LT(semver.MustParse("4.22.0")) || !capabilities.IsNodeTuningCapabilityEnabled(hostedCluster.Spec.Capabilities) {
		return false, nil
	}
	pools := &hyperv1.NodePoolList{}
	if err := tc.MgmtClient.List(tc.Context, pools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
		return false, err
	}
	if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform {
		total := int64(0)
		for _, pool := range pools.Items {
			if pool.Spec.ClusterName != hostedCluster.Name || pool.Spec.Replicas == nil {
				continue
			}
			if *pool.Spec.Replicas < 0 {
				return false, fmt.Errorf("NodePool %s/%s has negative desired replicas", pool.Namespace, pool.Name)
			}
			total += int64(*pool.Spec.Replicas)
		}
		return total > 0, nil
	}
	nodes := &corev1.NodeList{}
	if netutil.IsPublicHC(hostedCluster) {
		hostedClient, err := tc.GetHostedClusterClient(hostedCluster)
		if err != nil {
			return false, err
		}
		if err := hostedClient.List(tc.Context, nodes); err != nil {
			return false, err
		}
	}
	return healthHasWorkers(hostedCluster, pools.Items, nodes.Items), nil
}

func metricsNodeTuningProbe(ctx context.Context, probe func(context.Context) (string, error)) (string, error) {
	execContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return probe(execContext)
}

func metricsVectorSize(output string) (int, error) {
	var response struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string            `json:"resultType"`
			Result     []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return 0, err
	}
	if response.Status != "success" || response.Data.ResultType != "vector" || response.Data.Result == nil {
		return 0, fmt.Errorf("expected a successful Prometheus vector response")
	}
	return len(response.Data.Result), nil
}

func metricsForwarderTargetUp(output string) (bool, error) {
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Targets []struct {
				ScrapePool string `json:"scrapePool"`
				ScrapeURL  string `json:"scrapeUrl"`
				Health     string `json:"health"`
			} `json:"activeTargets"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return false, err
	}
	if response.Status != "success" {
		return false, fmt.Errorf("Prometheus targets request did not succeed")
	}
	for _, target := range response.Data.Targets {
		if strings.Contains(target.ScrapePool, "control-plane-metrics-forwarder") && strings.Contains(target.ScrapeURL, "/metrics/kube-apiserver") && target.Health == "up" {
			return true, nil
		}
	}
	return false, nil
}

func metricsDeploymentReady(deployment *appsv1.Deployment) bool {
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	return desired > 0 && deployment.Status.ObservedGeneration == deployment.Generation && deployment.Status.UpdatedReplicas == desired && deployment.Status.ReadyReplicas == desired && deployment.Status.AvailableReplicas == desired && deployment.Status.UnavailableReplicas == 0
}
