package etcdrecovery

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"go.etcd.io/etcd/client/pkg/v3/transport"
	etcdclient "go.etcd.io/etcd/client/v3"
)

const (
	DefaultEtcdClientTimeout = 1 * time.Minute
)

type options struct {
	etcdEndpoints      []string
	etcdClientCertFile string
	etcdClientKeyFile  string
	etcdCAFile         string
	kubeconfig         string
}

type endpointStatus struct {
	endpoint  string
	id        uint64
	errors    []string
	hash      uint32
	isLearner bool
}

func NewStartCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "etcd-recovery",
		Short:        "Executes etcd-recovery job",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}

	cmd.AddCommand(NewStatusCommand())

	return cmd
}

func NewStatusCommand() *cobra.Command {
	opts := options{
		etcdClientCertFile: "/etc/etcd/tls/client/etcd-client.crt",
		etcdClientKeyFile:  "/etc/etcd/tls/client/etcd-client.key",
		etcdCAFile:         "/etc/etcd/tls/etcd-ca/ca.crt",
	}

	cmd := &cobra.Command{
		Use:          "status",
		Short:        "Determine if the HCP etcd cluster is healthy",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT)
			defer cancel()
			return runStatus(ctx, opts)
		},
	}

	cmd.Flags().StringSliceVar(&opts.etcdEndpoints, "etcd-endpoints", []string{}, "endpoints of the etcd cluster to check and recover.")
	cmd.Flags().StringVar(&opts.etcdClientCertFile, "etcd-client-cert", "", "etcd client cert file.")
	cmd.Flags().StringVar(&opts.etcdClientKeyFile, "etcd-client-key", "", "etcd client cert key file.")
	cmd.Flags().StringVar(&opts.etcdCAFile, "etcd-ca-cert", "", "etcd trusted CA cert file.")
	cmd.MarkFlagRequired("etcd-endpoints")

	if len(opts.etcdEndpoints) != 3 {
		panic(fmt.Errorf("etcd-endpoints must have 3 endpoints, got %d: %v", len(opts.etcdEndpoints), opts.etcdEndpoints))
	}

	return cmd
}

func runStatus(ctx context.Context, opts options) error {
	fmt.Println("Checking etcd status")
	return nil
}

func checkEtcdStatus(ctx context.Context, opts options) error {
	//timeoutContext, cancel := context.WithTimeout(ctx, DefaultEtcdClientTimeout)
	//defer cancel()

	// Generate etcd client
	etcdClient, err := getEtcdClient(opts)
	if err != nil {
		return fmt.Errorf("failed to create etcd client: %w", err)
	}
	defer etcdClient.Close()

	// Recover etcd cluster details
	epStatuses := make([]endpointStatus, 0)
	for _, endpoint := range opts.etcdEndpoints {
		epStatus := endpointStatus{}
		response, err := etcdClient.HashKV(ctx, endpoint, 0)
		if err != nil {
			return fmt.Errorf("failed to hash etcd key-value store: %w", err)
		}
		status, err := etcdClient.Status(ctx, endpoint)
		if err != nil {
			return fmt.Errorf("failed to get etcd status: %w", err)
		}

		epStatus.endpoint = endpoint
		epStatus.id = response.Header.MemberId
		epStatus.hash = response.Hash
		epStatus.isLearner = status.IsLearner
		epStatus.errors = status.Errors
		epStatuses = append(epStatuses, epStatus)
	}

	// perform validations
	return nil
}

func getEtcdClient(opts options) (*etcdclient.Client, error) {
	tlsConfig := transport.TLSInfo{
		KeyFile:        opts.etcdClientKeyFile,
		CertFile:       opts.etcdClientCertFile,
		TrustedCAFile:  opts.etcdCAFile,
		ClientCertAuth: true,
	}
	cli, err := etcdclient.New(etcdclient.Config{
		Endpoints:   opts.etcdEndpoints,
		DialTimeout: 20 * time.Second,
		TLS:         tlsConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	return cli, nil
}

//podList := &corev1.PodList{}
//	if err := r.List(ctx, podList, client.InNamespace(hcpNS), client.MatchingLabels{
//		"app": "etcd",
//	}); err != nil {
//		return fmt.Errorf("failed to list etcd pods: %w", err)
//	}
//
//	var failingPods []corev1.Pod
//	for _, pod := range podList.Items {
//		for _, containerStatus := range pod.Status.ContainerStatuses {
//			if containerStatus.State.Waiting != nil && containerStatus.State.Waiting.Reason == "CrashLoopBackOff" {
//				failingPods = append(failingPods, pod)
//			}
//		}
//	}
//
//	// In a normal situation with no crashing pods, the flow ends here.
//	if len(failingPods) == 0 {
//		log.Info("no etcd pods are failing, skipping recovery procedure")
//		return nil
//	}
//
//	// In a special situation when more than 1 ETCD pod is in a crash loop, we skip the recovery procedure.
//	if len(failingPods) > 1 {
//		return fmt.Errorf("more than one etcd pod is failing, skipping recovery procedure because lack of quorum: %v", failingPods)
//	}
//
//	// Is STS being updated?
//	etcdSTS := &appsv1.StatefulSet{
//		ObjectMeta: metav1.ObjectMeta{
//			Name:      "etcd",
//			Namespace: hcpNS,
//		},
//	}
//
//	if err := r.Get(ctx, client.ObjectKeyFromObject(etcdSTS), etcdSTS); err != nil {
//		return fmt.Errorf("failed to get etcd statefulset: %w", err)
//	}
//
//	if etcdSTS.Status.CurrentRevision != etcdSTS.Status.UpdateRevision {
//		return fmt.Errorf("etcd statefulset is updating, skipping recovery procedure")
//	}
//
//	// Checks finished starting recovery procedure
//	etcdPV := &corev1.PersistentVolume{
//		ObjectMeta: metav1.ObjectMeta{
//			Name:      fmt.Sprintf("data-%s", failingPods[0].Name),
//			Namespace: hcpNS,
//		},
//	}
//
//	if err := r.Get(ctx, client.ObjectKeyFromObject(etcdPV), etcdPV); err != nil {
//		return fmt.Errorf("failed to get etcd persistent volume: %w", err)
//	}
//
//	if etcdPV.Spec.ClaimRef == nil {
//		return fmt.Errorf("etcd persistent volume has no claim reference: %v", etcdPV)
//	}
//
//	etcdPVC := &corev1.PersistentVolumeClaim{
//		ObjectMeta: metav1.ObjectMeta{
//			Name:      etcdPV.Spec.ClaimRef.Name,
//			Namespace: hcpNS,
//		},
//	}
//
//	if err := r.Get(ctx, client.ObjectKeyFromObject(etcdPVC), etcdPVC); err != nil {
//		return fmt.Errorf("failed to get etcd persistent volume claim: %w", err)
//	}
//
//	if err := r.Delete(ctx, etcdPVC); err != nil {
//		return fmt.Errorf("failed to delete etcd persistent volume claim: %w", err)
//	}
//
//	if err := r.Delete(ctx, etcdPV); err != nil {
//		return fmt.Errorf("failed to delete etcd persistent volume: %w", err)
//	}
//
//	if err := r.Delete(ctx, &failingPods[0]); err != nil {
//		return fmt.Errorf("failed to delete failing etcd pod: %w", err)
//	}
