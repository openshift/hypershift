package availabilityprober

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/pkg/version"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type options struct {
	target             string
	kubeconfig         string
	requiredAPIs       stringSetFlag
	requiredAPIsParsed []schema.GroupVersionKind
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "availability-prober",
	}
	opts := options{}
	cmd.Flags().StringVar(&opts.target, "target", "", "A http url to probe. The program will continue until it gets a http 2XX back.")
	cmd.Flags().StringVar(&opts.kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Required when --required-api is set")
	cmd.Flags().Var(&opts.requiredAPIs, "required-api", "An api that must be up before the program will be end. Can be passed multiple times, must be in group,version,kind format (e.G. operators.coreos.com,v1alpha1,CatalogSource)")

	log := zap.New(zap.UseDevMode(true), zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
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
		}

		check(log, url, time.Second, time.Second, opts.requiredAPIsParsed, discoveryClient)
	}

	return cmd
}

func check(log logr.Logger, target *url.URL, requestTimeout time.Duration, sleepTime time.Duration, requiredAPIs []schema.GroupVersionKind, discoveryClient discovery.DiscoveryInterface) {
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

		log.Info("Success", "statuscode", response.StatusCode)
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
