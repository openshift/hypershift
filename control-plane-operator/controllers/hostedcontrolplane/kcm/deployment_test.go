package kcm

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestKCMArgs(t *testing.T) {
	testCases := []struct {
		name     string
		p        *KubeControllerManagerParams
		expected []string
	}{
		{
			name: "Leader elect args get set correctly",
			p:    &KubeControllerManagerParams{},
			expected: []string{
				"--leader-elect-resource-lock=configmapsleases",
				"--leader-elect=true",
				// Contrary to everything else, KCM should not have an increased lease duration, see
				// https://github.com/openshift/cluster-kube-controller-manager-operator/pull/557#issuecomment-904648807
				"--leader-elect-retry-period=3s",
			},
		},
	}

	allowedDuplicateArgs := sets.NewString(
		"--controllers",
		"--feature-gates",
	)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			args := kcmArgs(tc.p)

			seen := sets.String{}
			for _, arg := range args {
				key := strings.Split(arg, "=")[0]
				if allowedDuplicateArgs.Has(key) {
					continue
				}
				if seen.Has(key) {
					t.Errorf("duplicate arg %s found", key)
				}
				seen.Insert(key)
			}

			argSet := sets.NewString(args...)
			for _, arg := range tc.expected {
				if !argSet.Has(arg) {
					t.Errorf("expected arg %s not found", arg)
				}
			}
		})
	}
}
