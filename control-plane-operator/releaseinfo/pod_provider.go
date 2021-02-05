package releaseinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"time"

	imageapi "github.com/openshift/api/image/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

var _ Provider = (*PodProvider)(nil)

// PodProvider finds the release image metadata for an image by launching a pod
// using the image and extracting the serialized ImageStream from the image
// filesystem assumed to be present at /release-manifests/image-references.
type PodProvider struct {
	Pods v1.PodInterface

	// TODO: consider something like ExpirationCache if performance becomes an issue
}

func (p *PodProvider) Lookup(ctx context.Context, image string) (releaseImage *ReleaseImage, err error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "image-lookup",
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "lookup",
					Image:   image,
					Command: []string{"/usr/bin/cat", "/release-manifests/image-references"},
				},
			},
		},
	}

	// Launch the pod and ensure we clean up regardless of outcome
	pod, err = p.Pods.Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create image lookup pod: %w", err)
	}
	defer func() {
		err := p.Pods.Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			err = fmt.Errorf("failed to delete image lookup pod %q: %w", pod.Name, err)
		}
	}()

	// Wait for the pod to reach a terminate state
	err = wait.PollImmediateUntil(1*time.Second, func() (bool, error) {
		pod, err := p.Pods.Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return true, nil
		case corev1.PodFailed:
			return true, fmt.Errorf("image lookup pod failed")
		default:
			return false, nil
		}
	}, ctx.Done())
	if err != nil {
		return nil, fmt.Errorf("failed waiting for image lookup pod %q: %w", pod.Name, err)
	}

	// Try and extract the pod's logs
	req := p.Pods.GetLogs(pod.Name, &corev1.PodLogOptions{})
	logs, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read image lookup pod %q logs: %w", pod.Name, err)
	}
	defer func() {
		err := logs.Close()
		if err != nil {
			err = fmt.Errorf("failed to close pod %q log stream: %w", pod.Name, err)
		}
	}()
	data, err := ioutil.ReadAll(logs)

	// The logs should be a serialized ImageStream resource
	var imageStream imageapi.ImageStream
	err = json.Unmarshal(data, &imageStream)
	if err != nil {
		return nil, fmt.Errorf("couldn't read image lookup pod %q logs as a serialized ImageStream: %w\nraw logs:\n%s", pod.Name, err, string(data))
	}
	releaseImage = &ReleaseImage{ImageStream: &imageStream}
	return
}
