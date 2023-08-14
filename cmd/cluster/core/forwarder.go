// source: https://github.com/openshift/oc/blob/bc2163c506ff27cda7ab907a715aeb1815389ead/pkg/cli/rsync/forwarder.go
package core

import (
	"io"
	"net/http"

	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// portForwarder starts port forwarding to a given pod
type portForwarder struct {
	Namespace string
	PodName   string
	Client    kubernetes.Interface
	Config    *restclient.Config
	Out       io.Writer
	ErrOut    io.Writer
}

// ForwardPorts will forward a set of ports from a pod, the stopChan will stop the forwarding
// when it's closed or receives a struct{}
func (f *portForwarder) ForwardPorts(ports []string, stopChan <-chan struct{}) error {
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
