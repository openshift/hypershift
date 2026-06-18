package util

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// KeycloakAdminClient provides methods to interact with Keycloak Admin REST API
type KeycloakAdminClient struct {
	BaseURL    string
	AdminToken string
	HTTPClient *http.Client
	AdminUser  string
	AdminPass  string
}

// KeycloakUser represents a Keycloak user
type KeycloakUser struct {
	Username      string               `json:"username"`
	Enabled       bool                 `json:"enabled"`
	FirstName     string               `json:"firstName,omitempty"`
	LastName      string               `json:"lastName,omitempty"`
	Email         string               `json:"email,omitempty"`
	EmailVerified bool                 `json:"emailVerified,omitempty"`
	Groups        []string             `json:"groups,omitempty"`
	Credentials   []KeycloakCredential `json:"credentials,omitempty"`
}

// KeycloakGroup represents a Keycloak group
type KeycloakGroup struct {
	Name string `json:"name"`
}

// KeycloakCredential represents a user password credential
type KeycloakCredential struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	Temporary bool   `json:"temporary"`
}

// NewKeycloakAdminClient creates a new Keycloak admin client
func NewKeycloakAdminClient(baseURL, adminUser, adminPass, caCertFile string) *KeycloakAdminClient {
	return &KeycloakAdminClient{
		BaseURL:   baseURL,
		AdminUser: adminUser,
		AdminPass: adminPass,
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		},
	}
}

// GetAdminToken obtains an admin access token
func (kc *KeycloakAdminClient) GetAdminToken(ctx context.Context) error {
	tokenURL := fmt.Sprintf("%s/realms/master/protocol/openid-connect/token", kc.BaseURL)

	formData := url.Values{
		"client_id":  []string{"admin-cli"},
		"grant_type": []string{"password"},
		"username":   []string{kc.AdminUser},
		"password":   []string{kc.AdminPass},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Body = io.NopCloser(strings.NewReader(formData.Encode()))

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to get admin token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to get admin token, status: %d, body: %s", resp.StatusCode, string(body))
	}

	var tokenResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to decode token response: %w", err)
	}

	accessToken, ok := tokenResp["access_token"].(string)
	if !ok {
		return fmt.Errorf("access_token not found in response")
	}

	kc.AdminToken = accessToken
	return nil
}

// CreateGroup creates a new group in Keycloak
func (kc *KeycloakAdminClient) CreateGroup(ctx context.Context, groupName string) (string, error) {
	groupURL := fmt.Sprintf("%s/admin/realms/master/groups", kc.BaseURL)

	group := KeycloakGroup{Name: groupName}
	groupJSON, err := json.Marshal(group)
	if err != nil {
		return "", fmt.Errorf("failed to marshal group: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, groupURL, strings.NewReader(string(groupJSON)))
	if err != nil {
		return "", fmt.Errorf("failed to create group request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to create group: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create group, status: %d, body: %s", resp.StatusCode, string(body))
	}

	// Extract group ID from Location header
	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("location header not found in response")
	}

	// Location format: https://host/admin/realms/master/groups/{groupId}
	parts := strings.Split(location, "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("failed to parse group ID from location: %s", location)
	}
	groupID := parts[len(parts)-1]

	return groupID, nil
}

// CreateUser creates a new user in Keycloak
func (kc *KeycloakAdminClient) CreateUser(ctx context.Context, user KeycloakUser) (string, error) {
	userURL := fmt.Sprintf("%s/admin/realms/master/users", kc.BaseURL)

	userJSON, err := json.Marshal(user)
	if err != nil {
		return "", fmt.Errorf("failed to marshal user: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, userURL, strings.NewReader(string(userJSON)))
	if err != nil {
		return "", fmt.Errorf("failed to create user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to create user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create user, status: %d, body: %s", resp.StatusCode, string(body))
	}

	// Extract user ID from Location header
	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("location header not found in response")
	}

	// Location format: https://host/admin/realms/master/users/{userId}
	parts := strings.Split(location, "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("failed to parse user ID from location: %s", location)
	}
	userID := parts[len(parts)-1]

	return userID, nil
}

// GenerateRandomPassword generates a random password
func GenerateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// SetupKeycloakAdminClientFromCluster retrieves Keycloak admin credentials from the cluster and creates an admin client
func SetupKeycloakAdminClientFromCluster(ctx context.Context, t *testing.T, mgtClient crclient.Client, config *ExtOIDCConfig) (*KeycloakAdminClient, error) {
	g := NewWithT(t)

	// Tests are ran on both AWS and Azure AKS clusters respectively.
	// However, Keycloak credentials are stored differently on both.

	// On AWS, both admin username and password credentials are stored
	// via a StatefulSet called 'keycloak' in the 'keycloak' namespace.

	// On AKS, the admin username is stored in a config map called
	// 'keycloak-env-vars' in 'keycloak' namespace via data.KC_BOOTSTAP_ADMIN_USERNAME,
	// and the admin password is stored in a secret called 'keycloak'
	// in the 'keycloak' namespace via data.admin-password .
	// https://github.com/bitnami/charts/tree/main/bitnami/keycloak/templates

	adminUser, adminPass := "", ""

	// Try AWS approach first: read from StatefulSet environment variables
	t.Logf("Retrieving Keycloak admin credentials from StatefulSet (AWS approach)")
	sts := &appsv1.StatefulSet{}
	err := mgtClient.Get(ctx, crclient.ObjectKey{
		Namespace: "keycloak",
		Name:      "keycloak",
	}, sts)
	if err == nil {
		// StatefulSet exists, try to read credentials from environment variables
		for _, env := range sts.Spec.Template.Spec.Containers[0].Env {
			if env.Name == "KC_BOOTSTRAP_ADMIN_USERNAME" {
				adminUser = env.Value
			}
			if env.Name == "KC_BOOTSTRAP_ADMIN_PASSWORD" {
				adminPass = env.Value
			}
		}
	}

	// If credentials not found in StatefulSet, try AKS approach: ConfigMap + Secret
	if adminUser == "" || adminPass == "" {
		t.Logf("Credentials not found in StatefulSet, trying AKS approach (ConfigMap + Secret)")

		// Get admin username from ConfigMap
		cm := &corev1.ConfigMap{}
		err = mgtClient.Get(ctx, crclient.ObjectKey{
			Namespace: "keycloak",
			Name:      "keycloak-env-vars",
		}, cm)
		if err == nil && cm.Data != nil {
			adminUser = cm.Data["KC_BOOTSTRAP_ADMIN_USERNAME"]
		}

		// Get admin password from Secret
		secret := &corev1.Secret{}
		err = mgtClient.Get(ctx, crclient.ObjectKey{
			Namespace: "keycloak",
			Name:      "keycloak",
		}, secret)
		if err == nil && secret.Data != nil {
			adminPass = string(secret.Data["admin-password"])
		}
	}

	// Verify we found both credentials
	if adminUser == "" || adminPass == "" {
		return nil, fmt.Errorf("could not find Keycloak admin credentials in StatefulSet (AWS) or ConfigMap+Secret (AKS)")
	}

	t.Logf("Successfully retrieved Keycloak admin credentials (username: %s)", adminUser)

	// Trim /realms/master from issuerURL
	baseURL := strings.TrimSuffix(config.IssuerURL, "/realms/master")
	kc := NewKeycloakAdminClient(baseURL, adminUser, adminPass, config.IssuerCABundleFile)

	// Verify access by getting admin token
	err = kc.GetAdminToken(ctx)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get admin token")

	t.Logf("Successfully created Keycloak admin client")
	return kc, nil
}
