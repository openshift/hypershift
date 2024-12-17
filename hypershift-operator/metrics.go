package main

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/pkg/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/supportedversion"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	OperatorInfoMetricName = "hypershift_operator_info"
)

// semantically constant - not supposed to be changed at runtime
var (
	latestSupportedVersion = supportedversion.LatestSupportedVersion.String()
	hypershiftVersion      = version.GetRevision()
	goVersion              = runtime.Version()
	goArch                 = runtime.GOARCH
)

func getOperatorImage(client crclient.Client) (string, string, error) {
	ctx := context.TODO()
	hypershiftNamespace := os.Getenv("MY_NAMESPACE")
	hypershiftPodName := os.Getenv("MY_NAME")
	hypershiftPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: hypershiftPodName, Namespace: hypershiftNamespace}}
	var err error
	image := "not found"
	imageId := "not found"

	if err = client.Get(ctx, crclient.ObjectKeyFromObject(hypershiftPod), hypershiftPod); err == nil {
		for _, c := range hypershiftPod.Status.ContainerStatuses {
			if c.Name == assets.HypershiftOperatorName {
				image = c.Image
				imageId = c.ImageID
			}
		}
	}

	return image, imageId, err
}

func setupOperatorInfoMetric(mgr manager.Manager) error {
	var image, imageId string

	// We need to create a new client because the manager one still does not have the cache started
	tmpClient, err := crclient.New(mgr.GetConfig(), crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("error creating a temporary client: %w", err)
	}

	// Grabbing the Image and ImageID from Operator
	if image, imageId, err = getOperatorImage(tmpClient); err != nil {
		if apierrors.IsNotFound(err) {
			log := mgr.GetLogger()
			log.Error(err, "pod not found, reporting empty image")
		} else {
			return err
		}
	}

	crmetrics.Registry.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: OperatorInfoMetricName,
			Help: "Metric to capture the current operator details of the management cluster",
			ConstLabels: prometheus.Labels{
				"version":                hypershiftVersion,
				"image":                  image,
				"imageId":                imageId,
				"latestSupportedVersion": latestSupportedVersion,
				"goVersion":              goVersion,
				"goArch":                 goArch,
			},
		}, func() float64 { return float64(1) }))

	return nil
}
