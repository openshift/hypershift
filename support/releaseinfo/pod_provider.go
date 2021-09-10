package releaseinfo

import (
	"context"
	"fmt"
	"io/ioutil"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"

	ctrl "sigs.k8s.io/controller-runtime"
)

var _ Provider = (*PodProvider)(nil)

// PodProvider finds the release image metadata for an image by launching a pod
// using the image and extracting the serialized ImageStream from the image
// filesystem assumed to be present at /release-manifests/image-references.
type PodProvider struct {
	Pods    v1.PodInterface
	Secrets v1.SecretInterface
}

func (p *PodProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (releaseImage *ReleaseImage, err error) {
	log := ctrl.LoggerFrom(ctx, "image-lookup", image)

	if len(image) == 0 {
		return nil, fmt.Errorf("image pull reference is blank, a value is required")
	}
	if len(pullSecret) == 0 {
		return nil, fmt.Errorf("pull secret is empty, a value is required")
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "image-lookup",
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: pullSecret,
		},
	}
	secret, err = p.Secrets.Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create image lookup pull secret: %w", err)
	}
	defer func() {
		err := p.Secrets.Delete(ctx, secret.Name, metav1.DeleteOptions{})
		if err != nil {
			log.Error(err, "failed to delete secret used for image lookup", "name", secret.Name, "namespace", secret.Namespace)
		}
	}()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "image-lookup",
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "read-image-references",
					Image:   image,
					Command: []string{"/usr/bin/cat", "/release-manifests/image-references"},
				},
				{
					Name:    "read-coreos-metadata",
					Image:   image,
					Command: []string{"/usr/bin/cat", "/release-manifests/0000_50_installer_coreos-bootimages.yaml"},
				},
			},
			ImagePullSecrets: []corev1.LocalObjectReference{
				{
					Name: secret.Name,
				},
			},
		},
	}

	// Launch the pod and ensure we clean up regardless of outcome
	pod, err = p.Pods.Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create image lookup pod: %w", err)
	}
	defer func() {
		err := p.Pods.Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			log.Error(err, "failed to delete image lookup pod", "name", pod.Name, "namespace", pod.Namespace)
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

	imageStreamData, err := getContainerLogs(ctx, p.Pods, pod.Name, "read-image-references")
	if err != nil {
		return nil, fmt.Errorf("couldn't lookup image reference metadata: %w", err)
	}
	osData, err := getContainerLogs(ctx, p.Pods, pod.Name, "read-coreos-metadata")
	if err != nil {
		return nil, fmt.Errorf("couldn't lookup coreos metadata: %w", err)
	}

	imageStream, err := DeserializeImageStream(imageStreamData)
	if err != nil {
		return nil, fmt.Errorf("couldn't read image lookup pod %q logs as a serialized ImageStream: %w", pod.Name, err)
	}

	coreOSMeta, err := DeserializeImageMetadata(osData)
	if err != nil {
		return nil, fmt.Errorf("couldn't read image lookup pod %q logs as a serialized ConfigMap: %w", pod.Name, err)
	}

	releaseImage = &ReleaseImage{
		ImageStream:    imageStream,
		StreamMetadata: coreOSMeta,
	}
	return
}

func getContainerLogs(ctx context.Context, pods v1.PodInterface, podName, containerName string) ([]byte, error) {
	req := pods.GetLogs(podName, &corev1.PodLogOptions{Container: containerName})
	logs, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read logs from %s/%s: %w", podName, containerName, err)
	}
	defer logs.Close()
	data, err := ioutil.ReadAll(logs)
	if err != nil {
		return nil, fmt.Errorf("couldn't decode logs from %s/%s: %w", podName, containerName, err)
	}
	return data, nil
}
