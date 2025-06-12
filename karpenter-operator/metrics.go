package main

import (
	"context"
	"fmt"
	"os"
	"runtime"

	karpenteroperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	karpenterassets "github.com/openshift/hypershift/karpenter-operator/controllers/karpenter/assets"
	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	goVersion = runtime.Version()
	goArch    = runtime.GOARCH
)

func getOperatorImage(client crclient.Client) (string, error) {
	ctx := context.TODO()
	karpenterNamespace := os.Getenv("MY_NAMESPACE")
	karpenterPodName := os.Getenv("MY_NAME")
	karpenterPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: karpenterPodName, Namespace: karpenterNamespace}}
	var err error
	image := "not found"

	if err = client.Get(ctx, crclient.ObjectKeyFromObject(karpenterPod), karpenterPod); err == nil {
		for _, c := range karpenterPod.Status.ContainerStatuses {
			if c.Name == karpenteroperatorv2.ComponentName {
				image = c.Image
			}
		}
	}

	return image, err
}

func setupOperatorInfoMetric(managementCluster cluster.Cluster) error {
	var image string

	// We need to create a new client because the manager one still does not have the cache started
	tmpClient, err := crclient.New(managementCluster.GetConfig(), crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("error creating a temporary client: %w", err)
	}

	// Grabbing the Image and ImageID from Operator
	if image, err = getOperatorImage(tmpClient); err != nil {
		if apierrors.IsNotFound(err) {
			klog.Error(err, "pod not found, reporting empty image")
		} else {
			return err
		}
	}

	crmetrics.Registry.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: karpenterassets.KarpenterOperatorInfoMetricName,
			Help: "Metric to capture the current Karpenter Operator details in the control plane",
			ConstLabels: prometheus.Labels{
				"image":     image,
				"goVersion": goVersion,
				"goArch":    goArch,
			},
		}, func() float64 { return float64(1) }))

	return nil
}
