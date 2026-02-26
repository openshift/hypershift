package oauth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
)

const (
	authOperatorBinaryPath = "/usr/local/bin/operators/authentication-operator"
)

type ManifestGenerator struct {
	hcp *hyperv1.HostedControlPlane
}

func NewManifestGenerator(hcp *hyperv1.HostedControlPlane) *ManifestGenerator {
	return &ManifestGenerator{
		hcp: hcp,
	}
}

func (m *ManifestGenerator) GenerateOAuthDeployment(ctx context.Context) (*appsv1.Deployment, error) {
	hash := computeHash(m.hcp.Namespace, m.hcp.Name)
	tmpDir := filepath.Join("/tmp", fmt.Sprintf("oauth-gen-%s", hash))
	inputDir := filepath.Join(tmpDir, "input")
	outputDir := filepath.Join(tmpDir, "output")

	fmt.Printf("Generating OAuth deployment using authentication-operator %s/%s\n", m.hcp.Namespace, m.hcp.Name)
	fmt.Printf("input-dir: %s, output-dir: %s\n", inputDir, outputDir)

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create input directory: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Printf("Executing authentication-operator at %s\n", authOperatorBinaryPath)
	cmd := exec.CommandContext(ctx, authOperatorBinaryPath,
		"apply-configuration",
		"--input-dir", inputDir,
		"--output-dir", outputDir,
		"--controllers", "HyperShiftOAuthServerController",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to execute authentication-operator: %w\nOutput: %s", err, string(output))
	}
	fmt.Printf("Successfully executed authentication-operator: %d bytes\n", len(output))
	if len(output) > 0 {
		fmt.Printf("authentication-operator output:\n%s\n", string(output))
	}

	deploymentPath, err := findDeploymentManifest(outputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to find deployment manifest: %w", err)
	}

	deployment, err := parseDeploymentManifest(deploymentPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse deployment manifest: %w", err)
	}

	// todo: check if neede
	// deployment.Namespace = ""

	return deployment, nil
}

func findDeploymentManifest(outputDir string) (string, error) {
	var foundManifests []string

	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if strings.Contains(path, "openshift-authentication/apps/deployments") &&
			strings.Contains(info.Name(), "body") &&
			strings.HasSuffix(info.Name(), ".yaml") {
			foundManifests = append(foundManifests, path)
		}

		return nil
	})

	if err != nil {
		return "", fmt.Errorf("error walking output directory: %w", err)
	}

	if len(foundManifests) == 0 {
		return "", fmt.Errorf("no deployment manifest found in output directory")
	}

	if len(foundManifests) > 1 {
		return "", fmt.Errorf("multiple deployment manifests found: %v", foundManifests)
	}

	return foundManifests[0], nil
}

func parseDeploymentManifest(manifestPath string) (*appsv1.Deployment, error) {
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file: %w", err)
	}

	deployment := &appsv1.Deployment{}
	if _, _, err := api.YamlSerializer.Decode(manifestBytes, nil, deployment); err != nil {
		return nil, fmt.Errorf("failed to decode deployment manifest: %w", err)
	}

	return deployment, nil
}

func computeHash(namespace, name string) string {
	h := sha256.New()
	h.Write([]byte(namespace + "/" + name))
	return fmt.Sprintf("%x", h.Sum(nil))[:8]
}
