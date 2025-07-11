package kms

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
	kmsapi "k8s.io/kms/apis/v2"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

const (
	kmsProxyImage = "quay.io/openshift/origin-kms-proxy:latest" // This would need to be a real image
	kmsProxyPort  = 8443
)

// KMSProxy represents a proxy that bridges Unix socket requests to network-based KMS services
type KMSProxy struct {
	unixSocketPath string
	targetEndpoint string
}

// NewKMSProxy creates a new KMS proxy instance (only supports KMS v2)
func NewKMSProxy(unixSocketPath, targetEndpoint string) *KMSProxy {
	return &KMSProxy{
		unixSocketPath: unixSocketPath,
		targetEndpoint: targetEndpoint,
	}
}

// KMSProxyContainer creates a container spec for the KMS proxy
func (p *KMSProxy) KMSProxyContainer(name string) corev1.Container {
	return corev1.Container{
		Name:  name,
		Image: kmsProxyImage,
		Args: []string{
			"--unix-socket-path=" + p.unixSocketPath,
			"--target-endpoint=" + p.targetEndpoint,
			"--api-version=v2", // Only support KMS v2
			"--v=2",
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("20Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      kasVolumeKMSSocket().Name,
				MountPath: "/opt",
			},
		},
	}
}

// KMSProxyServer implements the actual proxy server logic
type KMSProxyServer struct {
	unixSocketPath string
	targetEndpoint string
	server         *grpc.Server
}

// NewKMSProxyServer creates a new KMS proxy server (only supports KMS v2)
func NewKMSProxyServer(unixSocketPath, targetEndpoint string) *KMSProxyServer {
	return &KMSProxyServer{
		unixSocketPath: unixSocketPath,
		targetEndpoint: targetEndpoint,
	}
}

// Start starts the KMS proxy server
func (s *KMSProxyServer) Start(ctx context.Context) error {
	// Remove existing socket file if it exists
	if err := removeSocketFile(s.unixSocketPath); err != nil {
		return fmt.Errorf("failed to remove existing socket file: %v", err)
	}

	// Create Unix socket listener
	listener, err := net.Listen("unix", s.unixSocketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on unix socket %s: %v", s.unixSocketPath, err)
	}
	defer listener.Close()

	// Create gRPC server
	s.server = grpc.NewServer()

	// Create KMS proxy service with persistent connection
	proxyService, err := newKMSProxyService(s.targetEndpoint)
	if err != nil {
		return fmt.Errorf("failed to create KMS proxy service: %v", err)
	}
	defer proxyService.Close()

	// Register KMS v2 service
	kmsapi.RegisterKeyManagementServiceServer(s.server, proxyService)

	klog.Infof("KMS proxy server started, listening on %s, forwarding to %s", s.unixSocketPath, s.targetEndpoint)

	// Start serving
	errChan := make(chan error, 1)
	go func() {
		errChan <- s.server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		s.server.GracefulStop()
		return ctx.Err()
	case err := <-errChan:
		return err
	}
}

// removeSocketFile removes the socket file if it exists
func removeSocketFile(socketPath string) error {
	if _, err := os.Stat(socketPath); err == nil {
		return os.Remove(socketPath)
	}
	return nil
}

// kmsProxyService implements the KMS v2 proxy service
type kmsProxyService struct {
	targetEndpoint string
	client         kmsapi.KeyManagementServiceClient
	conn           *grpc.ClientConn
}

// newKMSProxyService creates a new KMS proxy service with a persistent connection
func newKMSProxyService(targetEndpoint string) (*kmsProxyService, error) {
	// Establish persistent gRPC connection with proper options using new API
	conn, err := grpc.NewClient(targetEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
		// Add keepalive to maintain connection health
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client for target endpoint %s: %v", targetEndpoint, err)
	}

	return &kmsProxyService{
		targetEndpoint: targetEndpoint,
		client:         kmsapi.NewKeyManagementServiceClient(conn),
		conn:           conn,
	}, nil
}

// Close closes the gRPC connection
func (s *kmsProxyService) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *kmsProxyService) Status(ctx context.Context, req *kmsapi.StatusRequest) (*kmsapi.StatusResponse, error) {
	return s.client.Status(ctx, req)
}

func (s *kmsProxyService) Encrypt(ctx context.Context, req *kmsapi.EncryptRequest) (*kmsapi.EncryptResponse, error) {
	return s.client.Encrypt(ctx, req)
}

func (s *kmsProxyService) Decrypt(ctx context.Context, req *kmsapi.DecryptRequest) (*kmsapi.DecryptResponse, error) {
	return s.client.Decrypt(ctx, req)
}
