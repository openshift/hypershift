package dnsresolver

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	var selfRegister bool

	cmd := &cobra.Command{
		Use:   "resolve-dns NAME",
		Short: "Utility that ensures a DNS name can be resolved.",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				fmt.Printf("Specify a DNS name to lookup\n")
				os.Exit(1)
			}
			if selfRegister {
				if err := selfRegisterEndpointSlice(args[0]); err != nil {
					fmt.Printf("Warning: self-registration failed, falling back to DNS-only: %v\n", err)
				}
			}
			if err := resolveDNS(context.Background(), args[0]); err != nil {
				fmt.Printf("Error: %v", err)
				os.Exit(1)
			}
		},
	}

	cmd.Flags().BoolVar(&selfRegister, "self-register", false, "Register this pod's IP into an EndpointSlice before resolving DNS")
	return cmd
}

func resolveDNS(ctx context.Context, hostName string) error {
	err := wait.PollUntilContextTimeout(ctx, time.Second, 10*time.Minute, false, func(ctx context.Context) (done bool, err error) {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, hostName)
		if err == nil && len(ips) > 0 {
			fmt.Printf("Address %s resolved to %s\n", hostName, ips[0].String())
			return true, nil
		}
		fmt.Printf("Address %s not resolved yet: %v\n", hostName, err)
		return false, nil
	})
	if err != nil {
		fmt.Printf("failed to resolve DNS name, giving up\n")
		return err
	}
	return nil
}

// selfRegisterEndpointSlice creates an EndpointSlice for this pod's IP
// so that CoreDNS can resolve the pod's DNS name without waiting for the
// standard EndpointSlice controller, which may have a stale informer cache
// under high cluster density.
func selfRegisterEndpointSlice(dnsName string) error {
	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		return fmt.Errorf("POD_IP environment variable not set")
	}
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		return fmt.Errorf("NAMESPACE environment variable not set")
	}
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to get hostname: %w", err)
	}

	serviceName, err := parseServiceName(dnsName)
	if err != nil {
		return fmt.Errorf("failed to parse service name from DNS name %q: %w", dnsName, err)
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to get in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pod, err := client.CoreV1().Pods(namespace).Get(ctx, hostname, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get pod %s/%s: %w", namespace, hostname, err)
	}

	addressType := discoveryv1.AddressTypeIPv4
	if net.ParseIP(podIP) != nil && net.ParseIP(podIP).To4() == nil {
		addressType = discoveryv1.AddressTypeIPv6
	}

	sliceName := fmt.Sprintf("%s-self-%s", serviceName, hostname)
	endpointSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sliceName,
			Namespace: namespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: serviceName,
				discoveryv1.LabelManagedBy:   "control-plane-operator",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "Pod",
					Name:       pod.Name,
					UID:        pod.UID,
				},
			},
		},
		AddressType: addressType,
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses:  []string{podIP},
				Hostname:   ptr.To(hostname),
				NodeName:   ptr.To(pod.Spec.NodeName),
				Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)},
				TargetRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Name:      pod.Name,
					Namespace: namespace,
					UID:       pod.UID,
				},
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Name: ptr.To("peer"), Port: ptr.To(int32(2380)), Protocol: ptr.To(corev1.ProtocolTCP)},
			{Name: ptr.To("etcd-client"), Port: ptr.To(int32(2379)), Protocol: ptr.To(corev1.ProtocolTCP)},
		},
	}

	existing, err := client.DiscoveryV1().EndpointSlices(namespace).Get(ctx, sliceName, metav1.GetOptions{})
	if err == nil {
		endpointSlice.ResourceVersion = existing.ResourceVersion
		_, err = client.DiscoveryV1().EndpointSlices(namespace).Update(ctx, endpointSlice, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update EndpointSlice %s: %w", sliceName, err)
		}
		fmt.Printf("Updated self-registration EndpointSlice %s with address %s\n", sliceName, podIP)
	} else {
		_, err = client.DiscoveryV1().EndpointSlices(namespace).Create(ctx, endpointSlice, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create EndpointSlice %s: %w", sliceName, err)
		}
		fmt.Printf("Created self-registration EndpointSlice %s with address %s\n", sliceName, podIP)
	}
	return nil
}

// parseServiceName extracts the service name from a headless service DNS name.
// Format: <hostname>.<service-name>.<namespace>.svc[.cluster.local]
func parseServiceName(dnsName string) (string, error) {
	parts := strings.Split(dnsName, ".")
	if len(parts) < 3 {
		return "", fmt.Errorf("expected at least 3 dot-separated components, got %d", len(parts))
	}
	return parts[1], nil
}
