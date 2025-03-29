package forwarder

import (
	"context"
	"fmt"
	"io"
	"net/http"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// portForwarder starts port forwarding to a given pod
type PortForwarder struct {
	Namespace string
	PodName   string
	Client    kubernetes.Interface
	Config    *restclient.Config
	Out       io.Writer
	ErrOut    io.Writer
}

// ForwardPorts will forward a set of ports from a pod, the stopChan will stop the forwarding
// when it's closed or receives a struct{}
func (f *PortForwarder) ForwardPorts(ports []string, stopChan <-chan struct{}) error {
	req := f.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(f.Namespace).
		Name(f.PodName).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(f.Config)
	if err != nil {
		return err
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())

	readyChan := make(chan struct{})
	fw, err := portforward.New(dialer, ports, stopChan, readyChan, f.Out, f.ErrOut)
	if err != nil {
		return err
	}
	errChan := make(chan error)
	go func() { errChan <- fw.ForwardPorts() }()
	select {
	case <-readyChan:
		return nil
	case err = <-errChan:
		return err
	}
}

func GetRunningKubeAPIServerPod(ctx context.Context, kbClient crclient.Client, cpNamespace string) (*corev1.Pod, error) {
	kubeAPIServerPodList := &corev1.PodList{}
	if err := kbClient.List(ctx, kubeAPIServerPodList, crclient.InNamespace(cpNamespace), crclient.MatchingLabels{"app": "kube-apiserver", hyperv1.ControlPlaneComponentLabel: "kube-apiserver"}); err != nil {
		return nil, fmt.Errorf("failed to list kube-apiserver pods in control plane namespace: %w", err)
	}
	var podToForward *corev1.Pod
	for i := range kubeAPIServerPodList.Items {
		pod := &kubeAPIServerPodList.Items[i]
		if pod.Status.Phase == corev1.PodRunning {
			podToForward = pod
			break
		}
	}
	if podToForward == nil {
		return nil, fmt.Errorf("did not find running kube-apiserver pod for guest cluster")
	}
	return podToForward, nil
}
