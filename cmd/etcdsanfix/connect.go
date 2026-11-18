package etcdsanfix

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"

	etcdclient "go.etcd.io/etcd/client/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const etcdClientPort = 2379

// outOfCluster reports whether etcd must be reached by port-forward rather than directly.
// Forced by --local; otherwise true when the in-pod CA file is absent (we are on a
// workstation). An explicit --etcd-endpoint means the caller is routing directly already.
func (o *options) outOfCluster() bool {
	if o.local {
		return true
	}
	if o.etcdEndpoint != "" {
		return false
	}
	if _, err := os.Stat(o.etcdCAFile); err == nil {
		return false
	}
	return true
}

func (o *options) modeLabel() string {
	if o.outOfCluster() {
		return "out-of-cluster (port-forward, certs from secrets)"
	}
	return "in-cluster (direct)"
}

// etcdDial opens a clientv3 to one etcd member. member is a pod name like "etcd-0"; ""
// means any started member, for cluster-wide calls. The returned endpoint is what to pass
// to Status; cleanup closes the client and tears down any port-forward, and the caller
// must always call it. In-cluster it dials the member's service DNS with the in-pod certs;
// out-of-cluster it reads the certs from secrets and port-forwards to the member's pod.
func (o *options) etcdDial(ctx context.Context, member string) (*etcdclient.Client, string, func(), error) {
	if !o.outOfCluster() {
		endpoint := o.memberEndpoint(member)
		cli, err := o.etcdClientFromFiles(endpoint)
		if err != nil {
			return nil, "", nil, err
		}
		return cli, endpoint, func() { _ = cli.Close() }, nil
	}

	if o.kube == nil {
		if err := o.setupKube(); err != nil {
			return nil, "", nil, err
		}
	}
	tlsCfg, err := o.etcdTLSFromSecrets(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	pod := member
	if pod == "" {
		pod, err = o.firstRunningEtcdPod(ctx)
		if err != nil {
			return nil, "", nil, err
		}
	}
	localPort, stop, err := o.portForward(ctx, pod)
	if err != nil {
		return nil, "", nil, err
	}
	endpoint := fmt.Sprintf("https://localhost:%d", localPort)
	cli, err := etcdclient.New(etcdclient.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: etcdDialTimeout,
		TLS:         tlsCfg,
	})
	if err != nil {
		stop()
		return nil, "", nil, err
	}
	return cli, endpoint, func() { _ = cli.Close(); stop() }, nil
}

// memberEndpoint is the direct (in-cluster) client URL for a member, or the etcd-client
// service when member is "". An explicit --etcd-endpoint overrides both.
func (o *options) memberEndpoint(member string) string {
	if o.etcdEndpoint != "" {
		return o.etcdEndpoint
	}
	if member == "" {
		return fmt.Sprintf("https://etcd-client.%s.svc:2379", o.namespace)
	}
	return fmt.Sprintf("https://%s.etcd-discovery.%s.svc:2379", member, o.namespace)
}

// etcdTLSFromSecrets reads the etcd client cert and the signer CA from the management
// cluster and builds a TLS config for the forwarded dial. ServerName is localhost, which
// the etcd server cert carries as a SAN, so verification passes over the port-forward.
func (o *options) etcdTLSFromSecrets(ctx context.Context) (*tls.Config, error) {
	if o.etcdTLS != nil {
		return o.etcdTLS, nil
	}
	caSecret := manifests.EtcdSignerSecret(o.namespace)
	if err := o.kube.Get(ctx, crclient.ObjectKeyFromObject(caSecret), caSecret); err != nil {
		return nil, fmt.Errorf("get etcd-signer secret: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caSecret.Data[certs.CASignerCertMapKey]) {
		return nil, fmt.Errorf("etcd-signer %q is not valid PEM", certs.CASignerCertMapKey)
	}
	clientSecret := manifests.EtcdClientSecret(o.namespace)
	if err := o.kube.Get(ctx, crclient.ObjectKeyFromObject(clientSecret), clientSecret); err != nil {
		return nil, fmt.Errorf("get etcd-client secret: %w", err)
	}
	cert, err := tls.X509KeyPair(clientSecret.Data[pki.EtcdClientCrtKey], clientSecret.Data[pki.EtcdClientKeyKey])
	if err != nil {
		return nil, fmt.Errorf("etcd client keypair: %w", err)
	}
	o.etcdTLS = &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{cert},
		ServerName:   "localhost",
	}
	return o.etcdTLS, nil
}

// firstRunningEtcdPod returns the first Running etcd-<i> pod, for cluster-wide calls.
func (o *options) firstRunningEtcdPod(ctx context.Context) (string, error) {
	for i := 0; i < 7; i++ {
		name := fmt.Sprintf("etcd-%d", i)
		pod := &corev1.Pod{}
		if err := o.kube.Get(ctx, crclient.ObjectKey{Namespace: o.namespace, Name: name}, pod); err != nil {
			continue
		}
		if pod.Status.Phase == corev1.PodRunning {
			return name, nil
		}
	}
	return "", fmt.Errorf("no Running etcd pod found in %s to port-forward to", o.namespace)
}

// portForward opens a port-forward to podName's etcd client port and returns the chosen
// local port plus a stop func. The dial target is https://localhost:<localPort>.
func (o *options) portForward(ctx context.Context, podName string) (int, func(), error) {
	transport, upgrader, err := spdy.RoundTripperFor(o.restConfig)
	if err != nil {
		return 0, nil, err
	}
	serverURL, err := url.Parse(o.restConfig.Host)
	if err != nil {
		return 0, nil, fmt.Errorf("parse api server url: %w", err)
	}
	serverURL.Path = path.Join(serverURL.Path, "/api/v1/namespaces", o.namespace, "pods", podName, "portforward")
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, serverURL)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", etcdClientPort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return 0, nil, err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- fw.ForwardPorts() }()
	select {
	case <-readyCh:
	case err := <-errCh:
		return 0, nil, fmt.Errorf("port-forward to %s: %w", podName, err)
	case <-ctx.Done():
		close(stopCh)
		return 0, nil, ctx.Err()
	}
	ports, err := fw.GetPorts()
	if err != nil {
		close(stopCh)
		return 0, nil, err
	}
	return int(ports[0].Local), func() { close(stopCh) }, nil
}
