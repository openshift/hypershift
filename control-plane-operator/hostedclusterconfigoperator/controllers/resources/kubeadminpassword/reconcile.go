package kubeadminpassword

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"golang.org/x/crypto/bcrypt"
)

func ReconcileKubeadminPasswordHashSecret(secret *corev1.Secret, passwordSecret *corev1.Secret) error {
	password := passwordSecret.Data["password"]
	if secret.Data != nil {
		hash, hasHash := secret.Data["kubeadmin"]
		if hasHash && len(hash) > 0 {
			if bcrypt.CompareHashAndPassword(hash, password) == nil {
				return nil
			}
		}
	}
	passwordHash, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to generate password hash: %w", err)
	}
	secret.Data = map[string][]byte{"kubeadmin": passwordHash}
	return nil
}
