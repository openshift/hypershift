package azure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/support/forwarder"
	"github.com/openshift/hypershift/support/netutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var errNoHostedNode = errors.New("AzureMachine has no matching hosted Node")

// initializeAPIJournalCollector bounds startup even when the port-forward
// transport is blocked in its handshake. A late result is always cleaned up.
func initializeAPIJournalCollector(ctx context.Context, factory apiJournalFactory) (JournalCollector, func(), error) {
	type result struct {
		collector JournalCollector
		cleanup   func()
		err       error
	}
	ready := make(chan result)
	go func() {
		collector, cleanup, err := factory(ctx)
		select {
		case ready <- result{collector, cleanup, err}:
		case <-ctx.Done():
			if cleanup != nil {
				cleanup()
			}
		}
	}()
	select {
	case r := <-ready:
		return r.collector, r.cleanup, r.err
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}

// newAPIJournalCollector uses the localhost kubeconfig and management API
// port-forward for private clusters. The tunnel lifetime is independent of the
// setup timeout and ends on cleanup or collection cancellation.
func newAPIJournalCollector(setupCtx, lifetimeCtx context.Context, management client.Client, hc *hyperv1.HostedCluster, managementConfig *rest.Config) (JournalCollector, func(), error) {
	var secret corev1.Secret
	var kubeconfig []byte
	var stopForward func()
	if managementConfig != nil {
		ns := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
		if err := management.Get(setupCtx, client.ObjectKey{Namespace: ns, Name: "localhost-kubeconfig"}, &secret); err != nil {
			return nil, nil, errors.New("localhost kubeconfig is unavailable")
		}
		pod, err := forwarder.GetRunningKubeAPIServerPod(setupCtx, management, ns)
		if err != nil {
			return nil, nil, errors.New("hosted kube-apiserver is unavailable")
		}
		config := rest.CopyConfig(managementConfig)
		config.Timeout = 30 * time.Second
		kubeClient, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, nil, errors.New("failed to configure API port-forward")
		}
		stop := make(chan struct{})
		var once sync.Once
		stopForward = func() { once.Do(func() { close(stop) }) }
		stopOnCancel := context.AfterFunc(lifetimeCtx, stopForward)
		cleanupForward := stopForward
		stopForward = func() { stopOnCancel(); cleanupForward() }
		fwd := &forwarder.PortForwarder{Namespace: pod.Namespace, PodName: pod.Name, Client: kubeClient, Config: config, Out: io.Discard, ErrOut: io.Discard}
		// Stop the tunnel if its startup hangs, without ending a successful
		// tunnel when the shorter setup context is subsequently canceled.
		stopOnSetupCancel := context.AfterFunc(setupCtx, cleanupForward)
		err = fwd.ForwardPorts([]string{fmt.Sprintf("0:%d", netutil.KASPodPortFromHostedCluster(hc))}, stop)
		stopOnSetupCancel()
		if err != nil {
			stopForward()
			return nil, nil, errors.New("failed to start hosted API port-forward")
		}
		ports, err := fwd.GetPorts()
		if err != nil || len(ports) != 1 {
			stopForward()
			return nil, nil, errors.New("failed to resolve hosted API port-forward port")
		}
		loaded, err := clientcmd.Load(secret.Data["kubeconfig"])
		if err != nil || len(loaded.Clusters) == 0 {
			stopForward()
			return nil, nil, errors.New("localhost kubeconfig is invalid")
		}
		for _, cluster := range loaded.Clusters {
			cluster.Server = "https://" + net.JoinHostPort("localhost", fmt.Sprint(ports[0].Local))
		}
		kubeconfig, err = clientcmd.Write(*loaded)
		if err != nil {
			stopForward()
			return nil, nil, errors.New("failed to configure hosted API kubeconfig")
		}
	} else {
		if hc.Status.KubeConfig == nil {
			return nil, nil, errors.New("hosted kubeconfig is unavailable")
		}
		if err := management.Get(setupCtx, client.ObjectKey{Namespace: hc.Namespace, Name: hc.Status.KubeConfig.Name}, &secret); err != nil {
			return nil, nil, errors.New("hosted kubeconfig secret is unavailable")
		}
		kubeconfig = secret.Data["kubeconfig"]
	}
	cleanup := func() {
		if stopForward != nil {
			stopForward()
		}
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		cleanup()
		return nil, nil, errors.New("hosted kubeconfig is invalid")
	}
	config.Timeout = 30 * time.Second
	nodeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		cleanup()
		return nil, nil, errors.New("failed to configure hosted node client")
	}
	nodes, err := nodeClient.CoreV1().Nodes().List(setupCtx, metav1.ListOptions{})
	if err != nil {
		cleanup()
		return nil, nil, errors.New("failed to list hosted Nodes")
	}
	file, err := os.CreateTemp("", "azure-journal-kubeconfig-*")
	if err != nil {
		cleanup()
		return nil, nil, errors.New("failed to create temporary journal kubeconfig")
	}
	name := file.Name()
	_, writeErr := file.Write(kubeconfig)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		cleanup()
		_ = os.Remove(name)
		return nil, nil, errors.New("failed to write journal kubeconfig")
	}
	return apiJournalCollectorForNodes(nodes.Items, name), func() { cleanup(); _ = os.Remove(name) }, nil
}

func apiJournalCollectorForNodes(nodes []corev1.Node, kubeconfigFile string) JournalCollector {
	byProviderID := make(map[string]string, len(nodes))
	for _, node := range nodes {
		if id := normalizedProviderID(node.Spec.ProviderID); id != "" {
			byProviderID[id] = node.Name
		}
	}
	return func(ctx context.Context, machine capiazure.AzureMachine, kind string, out io.Writer) error {
		if machine.Spec.ProviderID == nil {
			return errors.New("machine provider ID is missing")
		}
		node := byProviderID[normalizedProviderID(*machine.Spec.ProviderID)]
		if node == "" {
			return errNoHostedNode
		}
		args := []string{"adm", "node-logs", node, "--kubeconfig=" + kubeconfigFile, "--path=journal", "--boot=0", "--tail=100000", "--raw"}
		if kind != "journal" {
			args = append(args, "--unit="+kind)
		}
		return runJournalCommand(ctx, "oc", args, out)
	}
}
