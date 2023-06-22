package util

import (
	"context"
	"fmt"
	"io"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	k8sScheme "k8s.io/client-go/kubernetes/scheme"
	corev1Client "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// RemoteExecutor defines the interface accepted by the Exec command - provided for test stubbing
type RemoteExecutor interface {
	Execute(method string, url *url.URL, config *restclient.Config, stdin io.Reader, stdout, stderr io.Writer, tty bool) error
}

// DefaultRemoteExecutor is the standard implementation of remote command execution
type DefaultRemoteExecutor struct{}

func (*DefaultRemoteExecutor) Execute(method string, url *url.URL, config *restclient.Config, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
	exec, err := remotecommand.NewSPDYExecutor(config, method, url)
	if err != nil {
		return err
	}

	return exec.Stream(remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    tty,
	})
}

type StreamOptions struct {
	Stdin bool
	TTY   bool

	genericclioptions.IOStreams
}

// PodExecOptions declare the arguments accepted by the Exec command
type PodExecOptions struct {
	StreamOptions

	Command []string

	PodName       string
	Namespace     string
	ContainerName string

	Executor RemoteExecutor
	Config   *restclient.Config
}

// Validate checks that the provided exec options are specified.
func (p *PodExecOptions) Validate() error {
	if p.PodName == "" {
		return fmt.Errorf("podname must be specified")
	}
	if len(p.ContainerName) == 0 {
		return fmt.Errorf("containername must be specified")
	}
	if len(p.Command) == 0 {
		return fmt.Errorf("you must specify at least one command for the container")
	}
	if p.Out == nil || p.ErrOut == nil {
		return fmt.Errorf("both output and error output must be provided")
	}

	if p.Namespace == "" {
		p.Namespace = "default"
	}
	if p.Executor == nil {
		p.Executor = &DefaultRemoteExecutor{}
	}

	return nil
}

// Run executes a validated remote execution against a pod.
func (p *PodExecOptions) Run() error {
	err := p.Validate()
	if err != nil {
		return err
	}

	client, err := corev1Client.NewForConfig(p.Config)
	if err != nil {
		return err
	}

	pod, err := client.Pods(p.Namespace).Get(context.TODO(), p.PodName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return fmt.Errorf("cannot exec into a container in a completed pod; current phase is %s", pod.Status.Phase)
	}

	containerName := p.ContainerName

	req := client.RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Command:   p.Command,
		Stdin:     p.Stdin,
		Stdout:    p.Out != nil,
		Stderr:    p.ErrOut != nil,
		TTY:       p.TTY,
	}, k8sScheme.ParameterCodec)

	return p.Executor.Execute("POST", req.URL(), p.Config, p.In, p.Out, p.ErrOut, p.TTY)
}
