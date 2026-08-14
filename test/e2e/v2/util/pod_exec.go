//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// RunCommandInPod returns an error rather than failing directly, allowing callers inside Eventually() to retry.
func RunCommandInPod(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config, namespace, podName, containerName string, command ...string) (string, error) {
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, k8sscheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(restConfig, http.MethodPost, req.URL())
	if err != nil {
		return "", fmt.Errorf("failed to create executor for pod %s/%s: %w", namespace, podName, err)
	}

	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("command failed in pod %s/%s container %s: %w (stderr: %s)", namespace, podName, containerName, err, stderr.String())
	}

	return stdout.String(), nil
}

// GetMetricsFromPod returns an error rather than failing directly, allowing callers inside Eventually() to retry.
func GetMetricsFromPod(ctx context.Context, clientset kubernetes.Interface, restConfig *rest.Config, namespace, podName, containerName string, port int) (map[string]*dto.MetricFamily, error) {
	url := fmt.Sprintf("http://localhost:%d/metrics", port)
	output, err := RunCommandInPod(ctx, clientset, restConfig, namespace, podName, containerName, "curl", "-sS", "-f", url)
	if err != nil {
		return nil, err
	}
	if len(output) == 0 {
		return nil, fmt.Errorf("no metrics returned from pod %s/%s port %d", namespace, podName, port)
	}

	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(output))
	if err != nil {
		return nil, fmt.Errorf("failed to parse metrics from pod %s/%s port %d: %w", namespace, podName, port, err)
	}
	return families, nil
}
