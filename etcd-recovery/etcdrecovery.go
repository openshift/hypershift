package etcdrecovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	hyperapi "github.com/openshift/hypershift/support/api"
	etcdclient "go.etcd.io/etcd/client/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cr "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultEtcdClientTimeout = 1 * time.Minute
	defaultDialTimeout       = 20 * time.Second
)

type options struct {
	etcdEndpoints      string
	etcdClientCertFile string
	etcdClientKeyFile  string
	etcdCAFile         string
	kubeconfig         string
}

type endpointStatus struct {
	endpoint  string
	name      string
	id        uint64
	errors    []string
	hash      uint32
	isLearner bool
	health    bool
}

func NewStartCommand() *cobra.Command {
	opts := options{
		etcdClientCertFile: "/etc/etcd/tls/client/etcd-client.crt",
		etcdClientKeyFile:  "/etc/etcd/tls/client/etcd-client.key",
		etcdCAFile:         "/etc/etcd/tls/etcd-ca/ca.crt",
	}

	cmd := &cobra.Command{
		Use:          "etcd-recovery",
		Short:        "Executes etcd-recovery job",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}

	cmd.Flags().StringVar(&opts.etcdEndpoints, "etcd-endpoints", "", "endpoints of the etcd cluster to check and recover.")
	cmd.Flags().StringVar(&opts.etcdClientCertFile, "etcd-client-cert", "", "etcd client cert file.")
	cmd.Flags().StringVar(&opts.etcdClientKeyFile, "etcd-client-key", "", "etcd client cert key file.")
	cmd.Flags().StringVar(&opts.etcdCAFile, "etcd-ca-cert", "", "etcd trusted CA cert file.")
	_ = cmd.MarkFlagRequired("etcd-endpoints")

	cmd.AddCommand(NewStatusCommand(opts))
	cmd.AddCommand(NewRecoverCommand(opts))

	return cmd
}

func NewRecoverCommand(opts options) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "recover",
		Short:        "Recover etcd cluster",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT)
			defer cancel()

			if len(strings.Split(opts.etcdEndpoints, ",")) != 3 {
				panic(fmt.Errorf("etcd-endpoints must have 3 endpoints, got %d: %v", len(strings.Split(opts.etcdEndpoints, ",")), opts.etcdEndpoints))
			}

			err, failingEndpoint, failingPod, kubeclient := runStatus(ctx, opts)
			if err != nil {
				if failingEndpoint != nil {
					fmt.Printf("failing etcd endpoint: %v\n", failingEndpoint)
				}
				if failingPod != nil {
					fmt.Printf("failing etcd pod: %v\n", failingPod)
				}
				return recoverEtcd(ctx, failingEndpoint, *failingPod, kubeclient)
			}

			return nil
		},
	}

	return cmd
}

func NewStatusCommand(opts options) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status",
		Short:        "Determine if the HCP etcd cluster is healthy",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT)
			defer cancel()

			if err, failingEndpoint, failingPod, _ := runStatus(ctx, opts); err != nil {
				if failingEndpoint != nil {
					fmt.Printf("failing etcd endpoint: %v\n", failingEndpoint)
				}
				if failingPod != nil {
					fmt.Printf("failing etcd pod: %v\n", failingPod)
				}

				return fmt.Errorf("ETCD cluster is unhealthy: %w", err)
			}

			return nil
		},
	}

	return cmd
}

func runStatus(ctx context.Context, opts options) (error, *endpointStatus, *corev1.Pod, crclient.Client) {
	fmt.Println("Checking etcd status")
	// Generate kube client
	kubeClient, err := getKubeClient()
	if err != nil {
		return fmt.Errorf("failed to get kube client: %w", err), nil, nil, nil
	}

	// Generate etcd client
	etcdClient, err := getEtcdClient(opts)
	if err != nil {
		return fmt.Errorf("failed to create etcd client: %w", err), nil, nil, kubeClient
	}
	defer etcdClient.Close()

	err, failingEndpoint, failingPod := checkEtcdStatus(ctx, opts, etcdClient, kubeClient)
	if err != nil {
		return fmt.Errorf("failed to check etcd status: %w", err), nil, nil, kubeClient
	}

	if failingEndpoint != nil || failingPod != nil {
		return fmt.Errorf("etcd cluster is unhealthy: %v", failingEndpoint), failingEndpoint, failingPod, kubeClient
	}

	fmt.Println("no etcd pods are failing, and no etcd endpoints are in error state, skipping recovery procedure")
	return nil, nil, nil, nil
}

func checkEtcdStatus(ctx context.Context, opts options, etcdClient *etcdclient.Client, k crclient.Client) (error, *endpointStatus, *corev1.Pod) {
	//timeoutContext, cancel := context.WithTimeout(ctx, defaultEtcdClientTimeout)
	//defer cancel()

	// Get etcd members
	etcdMemberList, err := etcdClient.MemberList(ctx)
	if err != nil {
		return fmt.Errorf("failed to get etcd member list: %w", err), nil, nil
	}

	epList := make(map[uint64]string, 0)
	for _, member := range etcdMemberList.Members {
		epList[member.ID] = member.Name
	}

	epStatuses := make([]endpointStatus, 0)
	// Correlate ETCD members with endpoints
	for _, endpoint := range strings.Split(opts.etcdEndpoints, ",") {
		epStatus := endpointStatus{}
		response, err := etcdClient.HashKV(ctx, endpoint, 0)
		if err != nil {
			return fmt.Errorf("failed to hash etcd key-value store: %w", err), nil, nil
		}
		status, err := etcdClient.Status(ctx, endpoint)
		if err != nil {
			return fmt.Errorf("failed to get etcd status: %w", err), nil, nil
		}

		epStatus.name = epList[response.Header.MemberId]
		epStatus.endpoint = endpoint
		epStatus.id = response.Header.MemberId
		epStatus.hash = response.Hash
		epStatus.isLearner = status.IsLearner
		epStatus.errors = status.Errors
		epStatus.health = len(status.Errors) == 0
		epStatuses = append(epStatuses, epStatus)
	}

	// perform ETCD Validations
	failingEndpoint, err := etcdValidate(epStatuses)
	if err != nil {
		return fmt.Errorf("failed to validate etcd cluster status: %w", err), nil, nil
	}

	if failingEndpoint == nil {
		fmt.Println("etcd cluster is healthy")
		return nil, nil, nil
	}

	// Verify if the failing member matches with the failing pod
	hcpNS := os.Getenv("NAMESPACE")
	podList := &corev1.PodList{}
	if err := k.List(ctx, podList, crclient.InNamespace(hcpNS), crclient.MatchingLabels{
		"app": "etcd",
	}); err != nil {
		return fmt.Errorf("failed to list etcd pods: %w", err), nil, nil
	}

	var failingPods []corev1.Pod
	for _, pod := range podList.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil && containerStatus.RestartCount > 0 && containerStatus.Name == "etcd" {
				failingPods = append(failingPods, pod)
			}
		}
	}

	// In a normal situation with no crashing pods, the flow ends here.
	if len(failingPods) == 0 {
		fmt.Println("no etcd pods are failing, skipping recovery procedure")
		return nil, nil, nil
	}

	// In a special situation when more than 1 ETCD pod is in a crash loop, we skip the recovery procedure.
	if len(failingPods) > 1 {
		return fmt.Errorf("more than one etcd pod is failing, skipping recovery procedure because lack of quorum: %v", failingPods), nil, nil
	}

	if failingPods[0].Name != failingEndpoint.name {
		return fmt.Errorf("failing etcd pod does not match with failing etcd endpoint: %v", failingPods[0]), nil, nil
	}

	// Is STS being updated?
	etcdSTS := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: hcpNS,
		},
	}

	if err := k.Get(ctx, crclient.ObjectKeyFromObject(etcdSTS), etcdSTS); err != nil {
		return fmt.Errorf("failed to get etcd statefulset: %w", err), nil, nil
	}

	if etcdSTS.Status.CurrentRevision != etcdSTS.Status.UpdateRevision {
		return fmt.Errorf("etcd statefulset is updating, skipping recovery procedure"), nil, nil
	}

	return nil, failingEndpoint, &failingPods[0]
}

// etcdValidate expects a list of endpointStatuses and validates the status of the etcd cluster.
// At this point the expectation is to have at least 3 endpoints and 1 of them in error state.
func etcdValidate(epStatuses []endpointStatus) (*endpointStatus, error) {
	var errorCount int
	var failingEndpoint endpointStatus

	if len(epStatuses) != 3 {
		return nil, fmt.Errorf("expected 3 etcd endpoints, got %d", len(epStatuses))
	}

	for _, epStatus := range epStatuses {
		if len(epStatus.errors) > 0 {
			errorCount++
			failingEndpoint = epStatus
		}
	}

	if errorCount <= 0 {
		fmt.Println("etcd cluster is healthy")
		return nil, nil
	}

	if errorCount > 1 {
		return nil, fmt.Errorf("expected 1 etcd endpoint in error state, got %d", errorCount)
	}

	// three different hashes are not expected
	if epStatuses[0].hash != epStatuses[1].hash && epStatuses[1].hash != epStatuses[2].hash {
		return nil, fmt.Errorf("etcd cluster hash mismatch: %v", epStatuses)
	}

	return &failingEndpoint, nil
}

func recoverEtcd(ctx context.Context, failingEndpoint *endpointStatus, failingPod corev1.Pod, k crclient.Client) error {
	fmt.Printf("Recovering etcd cluster, endpoint: %s name: %s\n", failingEndpoint.endpoint, failingEndpoint.name)
	hcpNS := os.Getenv("NAMESPACE")

	// Checks finished starting recovery procedure
	etcdPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("data-%s", failingPod.Name),
			Namespace: hcpNS,
		},
	}

	if err := k.Get(ctx, crclient.ObjectKeyFromObject(etcdPV), etcdPV); err != nil {
		return fmt.Errorf("failed to get etcd persistent volume: %w", err)
	}

	if etcdPV.Spec.ClaimRef == nil {
		return fmt.Errorf("etcd persistent volume has no claim reference: %v", etcdPV)
	}

	etcdPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      etcdPV.Spec.ClaimRef.Name,
			Namespace: hcpNS,
		},
	}

	if err := k.Get(ctx, crclient.ObjectKeyFromObject(etcdPVC), etcdPVC); err != nil {
		return fmt.Errorf("failed to get etcd persistent volume claim: %w", err)
	}

	if err := k.Delete(ctx, etcdPVC); err != nil {
		return fmt.Errorf("failed to delete etcd persistent volume claim: %w", err)
	}

	if err := k.Delete(ctx, etcdPV); err != nil {
		return fmt.Errorf("failed to delete etcd persistent volume: %w", err)
	}

	if err := k.Delete(ctx, &failingPod); err != nil {
		return fmt.Errorf("failed to delete failing etcd pod: %w", err)
	}

	return nil
}

func getEtcdClient(opts options) (*etcdclient.Client, error) {
	// Load CA
	caCert, err := os.ReadFile(opts.etcdCAFile)
	if err != nil {
		log.Fatalf("error reading ETCD CA certificate: %v", err)
	}
	rootCertPool := x509.NewCertPool()
	rootCertPool.AppendCertsFromPEM(caCert)

	// Create key-pair certificate
	clientCert, err := tls.LoadX509KeyPair(opts.etcdClientCertFile, opts.etcdClientKeyFile)
	if err != nil {
		return nil, fmt.Errorf("error loading key-pair certificate: %v", err)
	}

	// Create TLS configuration
	tlsConfig := tls.Config{
		InsecureSkipVerify: false,
		Certificates:       []tls.Certificate{clientCert},
		RootCAs:            rootCertPool,
	}

	cli, err := etcdclient.New(etcdclient.Config{
		Endpoints:   strings.Split(opts.etcdEndpoints, ","),
		DialTimeout: defaultDialTimeout,
		TLS:         &tlsConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	return cli, nil
}

func getKubeClient() (crclient.Client, error) {
	restConfig, err := cr.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get the restConfig for management cluster: %w", err)
	}

	kubeClient, err := crclient.New(restConfig, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}

	return kubeClient, nil
}
