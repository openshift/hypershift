package availabilityprober

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/pkg/version"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type options struct {
	target                        string
	kubeconfig                    string
	waitForInfrastructureResource bool
	waitForLabeledPodsGone        string
	waitForClusterRolebinding     string
	requiredAPIs                  stringSetFlag
	requiredAPIsParsed            []schema.GroupVersionKind
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "availability-prober",
	}
	opts := options{}
	cmd.Flags().StringVar(&opts.target, "target", "", "A http url to probe. The program will continue until it gets a http 2XX back.")
	cmd.Flags().StringVar(&opts.kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Required when --required-api is set")
	cmd.Flags().Var(&opts.requiredAPIs, "required-api", "An api that must be up before the program will be end. Can be passed multiple times, must be in group,version,kind format (e.G. operators.coreos.com,v1alpha1,CatalogSource)")
	cmd.Flags().BoolVar(&opts.waitForInfrastructureResource, "wait-for-infrastructure-resource", false, "Waits until the cluster infrastructure.config.openshift.io resource is present")
	cmd.Flags().StringVar(&opts.waitForLabeledPodsGone, "wait-for-labeled-pods-gone", "", "Waits until pods with the specified label is gone from the namespace. Must be in format: namespace/label=selector")
	cmd.Flags().StringVar(&opts.waitForClusterRolebinding, "wait-for-cluster-rolebinding", "", "Waits until a concrete ClusterRoleBinding is present.")

	log := zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))

	cmd.Run = func(cmd *cobra.Command, args []string) {
		log.Info("Starting availability-prober", "version", version.String())
		url, err := url.Parse(opts.target)
		if err != nil {
			log.Error(err, fmt.Sprintf("failed to parse %q as url", opts.target))
			os.Exit(1)
		}

		if len(opts.requiredAPIs.val) > 0 && opts.kubeconfig == "" {
			log.Info("--kubeconfig is mandatory when --required-api is passed")
			os.Exit(1)

		}
		opts.requiredAPIsParsed, err = parseGroupVersionKindArgValues(opts.requiredAPIs.val.List())
		if err != nil {
			log.Error(err, "failed to parse --required-api arguments")
			os.Exit(1)
		}

		var discoveryClient discovery.DiscoveryInterface
		var kubeClient crclient.Client
		if opts.kubeconfig != "" {
			restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
				&clientcmd.ClientConfigLoadingRules{ExplicitPath: opts.kubeconfig},
				&clientcmd.ConfigOverrides{},
			).ClientConfig()
			if err != nil {
				log.Error(err, "failed to get kubeconfig")
				os.Exit(1)
			}
			discoveryClient, err = discovery.NewDiscoveryClientForConfig(restConfig)
			if err != nil {
				log.Error(err, "failed to construct discovery client")
				os.Exit(1)
			}
			kubeClient, err = crclient.New(restConfig, crclient.Options{Scheme: hyperapi.Scheme})
			if err != nil {
				log.Error(err, "failed to construct controller-runtime client")
				os.Exit(1)
			}
		}

		check(log, url, time.Second, time.Second, opts.requiredAPIsParsed, opts.waitForInfrastructureResource, opts.waitForClusterRolebinding, opts.waitForLabeledPodsGone, discoveryClient, kubeClient)
	}

	return cmd
}

func check(log logr.Logger, target *url.URL, requestTimeout time.Duration, sleepTime time.Duration, requiredAPIs []schema.GroupVersionKind, waitForInfrastructureResource bool, waitForClusterRolebinding, waitForLabeledPodsGone string, discoveryClient discovery.DiscoveryInterface, kubeClient crclient.Client) {
	log = log.WithValues("sleepTime", sleepTime.String())
	client := &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	for ; ; time.Sleep(sleepTime) {
		response, err := client.Get(target.String())
		if err != nil {
			log.Error(err, "Request failed, retrying...")
			continue
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode > 299 {
			log.WithValues("statuscode", response.StatusCode).Info("Request didn't return a 2XX status code, retrying...")
			continue
		}
		log.Info("URI probing succeeded", "uri", target.String(), "statuscode", response.StatusCode)

		if len(requiredAPIs) > 0 {
			_, apis, err := discoveryClient.ServerGroupsAndResources()
			// Ignore GroupDiscoveryFailedError error, as the groups we care about might have been sucessfully discovered
			if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
				log.Error(err, "discovering api resources failed, retrying...")
				continue
			}
			var hasMissingAPIs bool
			for _, requiredAPI := range requiredAPIs {
				if !isAPIInAPIs(requiredAPI, apis) {
					log.Info("API not yet available, will retry", "gvk", requiredAPI.String())
					hasMissingAPIs = true
				}
			}
			if hasMissingAPIs {
				continue
			}
		}

		if waitForInfrastructureResource {
			var clusterInfrastructure configv1.Infrastructure
			err := kubeClient.Get(context.Background(), types.NamespacedName{Name: "cluster"}, &clusterInfrastructure)
			if err != nil {
				log.Info("cluster infrastructure resource not yet available", "err", err)
				continue
			}
			if clusterInfrastructure.Status.InfrastructureName == "" {
				log.Info("cluster infrastructure name is not yet set")
				continue
			}
		}

		if waitForLabeledPodsGone != "" {
			namespace := strings.Split(waitForLabeledPodsGone, "/")[0]
			labelSelectors := strings.TrimPrefix(waitForLabeledPodsGone, fmt.Sprintf("%s/", namespace))
			pods := &corev1.PodList{}
			labelSet, err := labels.ConvertSelectorToLabelsMap(labelSelectors)
			if err != nil {
				log.Error(err, fmt.Sprintf("invalid label selectors %s", labelSelectors))
				continue
			}
			err = kubeClient.List(context.Background(), pods, &crclient.ListOptions{
				Namespace:     namespace,
				LabelSelector: labels.SelectorFromValidatedSet(labelSet),
			})
			if err != nil && !apierrors.IsNotFound(err) {
				log.Error(err, fmt.Sprintf("failed to get pods with label %s in namespace %s, retrying...", labelSelectors, namespace))
				continue
			}
			if len(pods.Items) > 0 {
				var retry bool
				for _, pod := range pods.Items {
					if pod.DeletionTimestamp == nil || time.Since(pod.DeletionTimestamp.Time).Minutes() < float64(10) {
						retry = true
						break
					}
				}
				if retry {
					log.Info(fmt.Sprintf("pods %s in namespace %s still exist, retrying...", labelSelectors, namespace))
					continue
				}
			}
		}

		if len(waitForClusterRolebinding) > 0 {
			clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
			err := kubeClient.Get(context.Background(), types.NamespacedName{Name: waitForClusterRolebinding}, clusterRoleBinding)
			if err != nil {
				log.Info("failed to get cluster rolebinding, retrying...", "ClusterRoleBinding", waitForClusterRolebinding, "err", err)
				continue
			}
		}

		log.Info("all checks successful, exiting...")
		return
	}
}

type stringSetFlag struct {
	val sets.String
}

func (s *stringSetFlag) Set(v string) error {
	if s.val == nil {
		s.val = sets.String{}
	}
	s.val.Insert(v)
	return nil
}

func (s *stringSetFlag) String() string {
	return fmt.Sprintf("%v", s.val.List())
}
func (s *stringSetFlag) Type() string {
	return "stringSetFlag"
}

func parseGroupVersionKindArgValues(vals []string) ([]schema.GroupVersionKind, error) {
	var result []schema.GroupVersionKind
	var errs []error
	for _, val := range vals {
		parts := strings.Split(val, ",")
		if len(parts) != 3 {
			errs = append(errs, fmt.Errorf("--required-api %s doesn't have exactly three comma-separated elements", val))
			continue
		}
		result = append(result, schema.GroupVersionKind{
			Group:   parts[0],
			Version: parts[1],
			Kind:    parts[2],
		})
	}

	return result, utilerrors.NewAggregate(errs)
}

func isAPIInAPIs(api schema.GroupVersionKind, apis []*metav1.APIResourceList) bool {
	for _, item := range apis {
		if item.GroupVersion != api.GroupVersion().String() {
			continue
		}
		for _, apiResource := range item.APIResources {
			// The apiResources do not have the Group or Version field is set, that info is only present on the APIResourceList
			if apiResource.Kind == api.Kind {
				return true
			}
		}
	}

	return false
}
