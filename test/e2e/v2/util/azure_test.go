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
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestHasProjectedTokenVolume(t *testing.T) {
	tests := []struct {
		name    string
		volumes []corev1.Volume
		want    bool
	}{
		{
			name: "When the Azure token volume has a projected service account token, it should return true",
			volumes: []corev1.Volume{{
				Name: "azure-identity-token",
				VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{
					ServiceAccountToken: &corev1.ServiceAccountTokenProjection{},
				}}}},
			}},
			want: true,
		},
		{
			name: "When the volume has a different name, it should return false",
			volumes: []corev1.Volume{{
				Name:         "other-token",
				VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{}}}}},
			}},
			want: false,
		},
		{
			name: "When the Azure token volume has no service account token source, it should return false",
			volumes: []corev1.Volume{{
				Name:         "azure-identity-token",
				VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{{ConfigMap: &corev1.ConfigMapProjection{}}}}},
			}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasProjectedTokenVolume(tt.volumes); got != tt.want {
				t.Fatalf("hasProjectedTokenVolume() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestHasAzureFederatedTokenEnv(t *testing.T) {
	tests := []struct {
		name       string
		containers []corev1.Container
		want       bool
	}{
		{
			name: "When a container has the Azure federated token path, it should return true",
			containers: []corev1.Container{{Env: []corev1.EnvVar{{
				Name:  "AZURE_FEDERATED_TOKEN_FILE",
				Value: "/var/run/secrets/azure/tokens/azure-identity-token",
			}}}},
			want: true,
		},
		{
			name: "When the token environment variable has another path, it should return false",
			containers: []corev1.Container{{Env: []corev1.EnvVar{{
				Name:  "AZURE_FEDERATED_TOKEN_FILE",
				Value: "/var/run/secrets/tokens/token",
			}}}},
			want: false,
		},
		{
			name:       "When the container has no Azure federated token environment variable, it should return false",
			containers: []corev1.Container{{Env: []corev1.EnvVar{{Name: "OTHER", Value: "value"}}}},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasAzureFederatedTokenEnv(tt.containers); got != tt.want {
				t.Fatalf("hasAzureFederatedTokenEnv() = %t, want %t", got, tt.want)
			}
		})
	}
}
