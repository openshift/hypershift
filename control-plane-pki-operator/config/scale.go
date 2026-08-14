package config

import (
	"fmt"
	"os"
	"time"

	"k8s.io/klog/v2"
)

// defaultRotationDay is the default rotation base for all cert rotation operations.
const defaultRotationDay = 24 * time.Hour

func GetCertRotationScale() (time.Duration, error) {
	certRotationScale := defaultRotationDay
	if value := os.Getenv("CERT_ROTATION_SCALE"); value != "" {
		var err error
		certRotationScale, err = time.ParseDuration(value)
		if err != nil {
			return 0, fmt.Errorf("invalid format for $CERT_ROTATION_SCALE: %w", err)
		}
		if certRotationScale > 24*time.Hour {
			return 0, fmt.Errorf("scale longer than 24h is not allowed: %v", certRotationScale)
		}
		klog.Warningf("!!! UNSUPPORTED VALUE SET !!!")
		klog.Warningf("Certificate rotation base set to %q", certRotationScale)
	}
	return certRotationScale, nil
}
