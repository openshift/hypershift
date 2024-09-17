package etcdrecovery

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"

	hyperapi "github.com/openshift/hypershift/support/api"
	hyperutil "github.com/openshift/hypershift/support/util"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	etcdclient "go.etcd.io/etcd/client/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	defaultEtcdClientTimeout = 5 * time.Second
	defaultDialTimeout       = 3 * time.Second
	healthyClusterWait       = 5 * time.Minute
)

type options struct {
	etcdClientCertFile string
	etcdClientKeyFile  string
	etcdCAFile         string
	namespace          string
}

func NewRecoveryCommand() *cobra.Command {
	opts := options{
		etcdClientCertFile: "/etc/etcd/tls/client/etcd-client.crt",
		etcdClientKeyFile:  "/etc/etcd/tls/client/etcd-client.key",
		etcdCAFile:         "/etc/etcd/tls/etcd-ca/ca.crt",
	}

	cmd := &cobra.Command{
		Use:          "recover-etcd",
		Short:        "Commands to report on etcd status and recover member that is not functional",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
	}

	cmd.PersistentFlags().StringVar(&opts.etcdClientCertFile, "etcd-client-cert", opts.etcdClientCertFile, "etcd client cert file.")
	cmd.PersistentFlags().StringVar(&opts.etcdClientKeyFile, "etcd-client-key", opts.etcdClientKeyFile, "etcd client cert key file.")
	cmd.PersistentFlags().StringVar(&opts.etcdCAFile, "etcd-ca-cert", opts.etcdCAFile, "etcd trusted CA cert file.")
	cmd.PersistentFlags().StringVar(&opts.namespace, "namespace", "", "namespace of etcd cluster")

	err := cmd.MarkPersistentFlagRequired("namespace")
	if err != nil {
		cmd.PrintErrf("error setting up namespace flag as required: %v\n", err)
		os.Exit(1)
	}

	cmd.AddCommand(NewStatusCommand(&opts))
	cmd.AddCommand(NewRunCommand(&opts))

	return cmd
}

func NewRunCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "run",
		Short:        "Recover etcd cluster that has quorum",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT)
			defer cancel()
			ctx = setupCmdContext(ctx)
			err := runRecovery(ctx, *opts)
			if err != nil {
				ctrl.Log.Error(err, "Error occurred")
				os.Exit(1)
			}
		},
	}

	return cmd
}

func setupCmdContext(cmdContext context.Context) context.Context {
	ctrl.SetLogger(zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	})))
	ctx := ctrl.LoggerInto(cmdContext, ctrl.Log.WithName("etcd-recovery"))
	return ctx
}

type etcdStatus struct {
	failingPod    *string
	missingMember *string
	recoverable   bool
}

func (s *etcdStatus) isUnhealthy() bool {
	return s.missingMember != nil || s.failingPod != nil
}

func (s *etcdStatus) isRecoverable() bool {
	recoverable := s.recoverable
	if s.failingPod != nil && s.missingMember != nil {
		if *s.failingPod != *s.missingMember {
			recoverable = false
		}
	}
	return recoverable
}

func (s *etcdStatus) memberToRecover() string {
	if !s.isRecoverable() {
		return ""
	}
	if s.failingPod != nil {
		return *s.failingPod
	}
	if s.missingMember != nil {
		return *s.missingMember
	}
	return ""
}

func (s *etcdStatus) String() string {
	var status []string
	if s.missingMember != nil {
		status = append(status, fmt.Sprintf("missing member: %s", *s.missingMember))
	}
	if s.failingPod != nil {
		status = append(status, fmt.Sprintf("failing pod: %s", *s.failingPod))
	}
	if !s.recoverable {
		status = append(status, "broken: NOT RECOVERABLE")
	}
	if len(status) > 0 {
		return strings.Join(status, ",")
	}
	return "healthy"
}

func NewStatusCommand(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status",
		Short:        "Determine if etcd cluster is healthy",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT)
			defer cancel()
			ctx = setupCmdContext(ctx)
			log := ctrl.LoggerFrom(ctx)
			etcdStatus, err := runStatus(ctx, *opts)
			if err != nil {
				log.Error(err, "Error occurred")
				os.Exit(1)
			}
			log.Info("etcd status", "status", etcdStatus.String())
		},
	}

	return cmd
}

func runRecovery(ctx context.Context, opts options) error {
	log := ctrl.LoggerFrom(ctx)

	status, err := runStatus(ctx, opts)
	if err != nil {
		return err
	}
	if !status.isRecoverable() {
		return errors.New("etcd cluster does not have a recoverable status")
	}
	if !status.isUnhealthy() {
		return nil
	}

	if err := recoverEtcd(ctx, opts, *status); err != nil {
		return err
	}

	if err := waitForHealthyCluster(ctx, opts); err != nil {
		return err
	}

	log.Info("Successfully completed cluster recovery")

	return nil
}

func waitForHealthyCluster(ctx context.Context, opts options) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Waiting for etcd cluster to become healthy")
	timeOutCtx, cancel := context.WithTimeout(ctx, healthyClusterWait)
	defer cancel()
	return wait.PollUntilContextCancel(timeOutCtx, 30*time.Second, false, func(ctx context.Context) (done bool, err error) {
		status, err := runStatus(ctx, opts)
		if err != nil {
			return false, err
		}
		if !status.isUnhealthy() {
			log.Info("Cluster is healthy")
			return true, nil
		}
		return false, nil
	})
}

func runStatus(ctx context.Context, opts options) (*etcdStatus, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Checking etcd status")

	kclient, err := kubeClient()
	if err != nil {
		return nil, err
	}

	eclient, err := etcdClient(ctx, opts, "")
	if err != nil {
		return nil, err
	}

	return checkEtcdStatus(ctx, opts, eclient, kclient)
}

// missingMember determines which of the 3 etcd members is missing
// when 2 members are present
func missingMember(member1, member2 *etcdserverpb.Member) *string {
	names := sets.New[string]()
	for _, m := range []*etcdserverpb.Member{member1, member2} {
		names.Insert(m.Name)
	}
	for _, m := range []string{"etcd-0", "etcd-1", "etcd-2"} {
		if !names.Has(m) {
			return &m
		}
	}
	return nil
}

func checkEtcdStatus(ctx context.Context, opts options, etcdClient *etcdclient.Client, k crclient.Client) (*etcdStatus, error) {
	log := ctrl.LoggerFrom(ctx)

	result := &etcdStatus{recoverable: true}

	// Get etcd members
	log.Info("Fetching etcd member list")
	reqCtx, cancel := context.WithTimeout(ctx, defaultEtcdClientTimeout)
	defer cancel()
	etcdMemberList, err := etcdClient.MemberList(reqCtx)
	if err != nil {
		log.Error(err, "failed to get etcd member list")
		return nil, err
	}

	switch len(etcdMemberList.Members) {
	case 0:
		return nil, fmt.Errorf("no members were returned from etcd")
	case 1:
		return nil, fmt.Errorf("cluster only has 1 member, cannot be recovered")
	case 2:
		result.missingMember = missingMember(etcdMemberList.Members[0], etcdMemberList.Members[1])
		log.Info("Etcd cluster only has 2 members", "missing member", ptr.Deref(result.missingMember, ""))
	case 3:
		log.Info("Etcd cluster has 3 members")
	}

	memberHealth := map[string]bool{}
	// Determine member health by looking at their endpoint health
	for _, member := range etcdMemberList.Members {
		if len(member.ClientURLs) != 1 {
			return nil, fmt.Errorf("member %s does not have an expected client URL: %v", member.Name, member.ClientURLs)
		}
		clientURL := member.ClientURLs[0]
		memberHealth[member.Name] = isEndpointHealthy(ctx, opts, member.Name, clientURL)
	}
	result.recoverable = isRecoverableMemberHealth(ctx, memberHealth)
	if !result.recoverable {
		return result, nil
	}

	// Determine if the statefulset is updating
	etcdSTS := etcdStatefulSet(opts.namespace)
	if err := k.Get(ctx, crclient.ObjectKeyFromObject(etcdSTS), etcdSTS); err != nil {
		return nil, fmt.Errorf("failed to get etcd statefulset: %w", err)
	}

	if etcdSTS.Status.CurrentRevision != etcdSTS.Status.UpdateRevision {
		return nil, fmt.Errorf("etcd statefulset is updating, cannot perform recovery")
	}

	// Find failing pod
	podList := &corev1.PodList{}
	if err := k.List(ctx, podList, crclient.InNamespace(opts.namespace), crclient.MatchingLabels{
		"app": "etcd",
	}); err != nil {
		return nil, fmt.Errorf("failed to list etcd pods: %w", err)
	}

	var failingPods []corev1.Pod
	for _, pod := range podList.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.Name != "etcd" {
				continue
			}
			if containerStatus.State.Waiting != nil && containerStatus.RestartCount > 0 {
				log.Info("pod is unhealthy", "pod", client.ObjectKeyFromObject(&pod).String())
				failingPods = append(failingPods, pod)
			} else {
				log.Info("pod is healthy", "pod", client.ObjectKeyFromObject(&pod).String())
			}
		}
	}

	switch len(failingPods) {
	case 0:
		// Expected: all pods healthy
	case 1:
		result.failingPod = ptr.To(failingPods[0].Name)
	default:
		result.recoverable = false
		log.Info("More than one failing pod was found. Etcd cluster is not recoverable", "pods", podNames(failingPods))
	}
	return result, nil
}

func recoverEtcd(ctx context.Context, opt options, status etcdStatus) error {
	log := ctrl.LoggerFrom(ctx, "namespace", opt.namespace)
	log.Info("Recovering etcd cluster")

	log.Info("Obtaining etcd client")
	eclient, err := etcdClient(ctx, opt, "")
	if err != nil {
		log.Error(err, "failed to get etcd client")
		return err
	}

	if status.missingMember != nil {
		log.Info("Adding missing member back to cluster", "member", *status.missingMember)
		memberAddCtx, cancel := context.WithTimeout(ctx, defaultEtcdClientTimeout)
		defer cancel()
		_, err := eclient.MemberAdd(memberAddCtx, []string{fmt.Sprintf("https://%s.etcd-discovery.%s.svc:2380", *status.missingMember, opt.namespace)})
		if err != nil {
			log.Error(err, "unable to add missing member to etcd cluster")
			return err
		}
	}

	memberToRecover := status.memberToRecover()
	log.Info("Removing etcd pod and corresponding pvc of broken member", "member", memberToRecover)

	pvc := &corev1.PersistentVolumeClaim{}
	pvc.Namespace = opt.namespace
	pvc.Name = fmt.Sprintf("data-%s", memberToRecover)

	pod := &corev1.Pod{}
	pod.Namespace = opt.namespace
	pod.Name = memberToRecover

	kclient, err := kubeClient()
	if err != nil {
		log.Error(err, "failed to get kubernetes client")
		return err
	}
	log.Info("Deleting pvc", "pvc", crclient.ObjectKeyFromObject(pvc))
	if _, err := hyperutil.DeleteIfNeeded(ctx, kclient, pvc); err != nil {
		log.Error(err, "failed to delete pvc", "pvc", crclient.ObjectKeyFromObject(pvc))
	}

	log.Info("Deleting pod", "pod", crclient.ObjectKeyFromObject(pod))
	if _, err := hyperutil.DeleteIfNeeded(ctx, kclient, pod); err != nil {
		log.Error(err, "failed to delete pod", "pod", crclient.ObjectKeyFromObject(pod))
	}

	log.Info("Etcd recovery actions completed")

	return nil
}

func etcdClient(ctx context.Context, opts options, endpoint string) (*etcdclient.Client, error) {

	log := ctrl.LoggerFrom(ctx)
	log.Info("Creating etcd client")

	// Load CA
	log.Info("Loading etcd ca", "file", opts.etcdCAFile)
	caCert, err := os.ReadFile(opts.etcdCAFile)
	if err != nil {
		log.Error(err, "failed to read etcd ca file")
		return nil, err
	}
	rootCertPool := x509.NewCertPool()
	rootCertPool.AppendCertsFromPEM(caCert)

	// Create key-pair certificate
	log.Info("Loading etcd client certificate", "cert", opts.etcdClientCertFile, "key", opts.etcdClientKeyFile)
	clientCert, err := tls.LoadX509KeyPair(opts.etcdClientCertFile, opts.etcdClientKeyFile)
	if err != nil {
		log.Error(err, "failed to load etcd client cert")
		return nil, err
	}

	// Create TLS configuration
	tlsConfig := tls.Config{
		InsecureSkipVerify: false,
		Certificates:       []tls.Certificate{clientCert},
		RootCAs:            rootCertPool,
	}

	ep := fmt.Sprintf("https://etcd-client.%s.svc:2379", opts.namespace) // Default to etcd service endpoint, unless specified otherwise
	if endpoint != "" {
		ep = endpoint
	}

	log.Info("Creating client to etcd service", "endpoint", ep)
	cli, err := etcdclient.New(etcdclient.Config{
		Endpoints:   []string{ep},
		DialTimeout: defaultDialTimeout,
		TLS:         &tlsConfig,
	})
	if err != nil {
		log.Error(err, "failed to create etcd client")
		return nil, err
	}

	return cli, nil
}

func kubeClient() (crclient.Client, error) {
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get the restConfig for management cluster: %w", err)
	}

	kubeClient, err := crclient.New(restConfig, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}

	return kubeClient, nil
}

// isEndpointHealthy uses similar code to the implementation of 'etcdctl endpoint health' to
// determine if a particular etcd endpoint is healthy
func isEndpointHealthy(ctx context.Context, opts options, name, endpointURL string) bool {
	log := ctrl.LoggerFrom(ctx, "name", name, "url", endpointURL)

	log.Info("Checking endpoint health")

	cli, err := etcdClient(ctx, opts, endpointURL)
	if err != nil {
		log.Error(err, "cannot create etcd client, returning healthy=false")
		return false
	}
	defer cli.Close()

	healthCtx, healthCtxCancel := context.WithTimeout(ctx, defaultEtcdClientTimeout)
	defer healthCtxCancel()
	_, err = cli.Get(healthCtx, "health")
	if err != nil && err != rpctypes.ErrPermissionDenied {
		log.Error(err, "cannot access etcd endpoint, returning healthy=false")
		return false
	}

	alarmCtx, alarmCtxCancel := context.WithTimeout(ctx, defaultEtcdClientTimeout)
	defer alarmCtxCancel()
	resp, err := cli.AlarmList(alarmCtx)
	if err == nil && len(resp.Alarms) > 0 {
		log.Info("endpoint has active alarms, returning healthy=false")
		for _, v := range resp.Alarms {
			switch v.Alarm {
			case etcdserverpb.AlarmType_NOSPACE:
				log.Info("Alarm: NOSPACE")
			case etcdserverpb.AlarmType_CORRUPT:
				log.Info("Alarm: CORRUPT")
			default:
				log.Info("Alarm: UNKNOWN")
			}
		}
		return false
	} else if err != nil {
		log.Error(err, "Failed to fetch the alarm list, returning healthy=false")
		return false
	}

	log.Info("Endpoint is healthy")
	return true
}

func isRecoverableMemberHealth(ctx context.Context, memberHealth map[string]bool) bool {
	log := ctrl.LoggerFrom(ctx)
	switch len(memberHealth) {
	case 2:
		for m, healthy := range memberHealth {
			if !healthy {
				log.Info("etcd cluster is not recoverable, missing member and unhealthy member", "unhealthy", m)
				return false
			}
		}
	case 3:
		unHealthyCount := 0
		for _, healthy := range memberHealth {
			if !healthy {
				unHealthyCount++
			}
		}
		if unHealthyCount > 1 {
			log.Info("etcd cluster is not recoverable, more than one unhealthy member")
			return false
		}
	default:
		return false
	}
	return true
}

func etcdStatefulSet(ns string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: ns,
		},
	}
}

func podNames(pods []corev1.Pod) string {
	names := make([]string, 0, len(pods))
	for _, p := range pods {
		names = append(names, p.Name)
	}
	return strings.Join(names, ",")
}
