package tokenminter

import (
	"sort"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/spf13/pflag"
)

func TestNewStartCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, g Gomega)
	}{
		{
			name: "When token-minter command is created, it should have 'token-minter' as use",
			test: func(t *testing.T, g Gomega) {
				cmd := NewStartCommand()
				g.Expect(cmd.Use).To(Equal("token-minter"))
			},
		},
		{
			name: "When token-minter command is created, it should register exactly the expected flags",
			test: func(t *testing.T, g Gomega) {
				cmd := NewStartCommand()
				var flagNames []string
				cmd.Flags().VisitAll(func(f *pflag.Flag) {
					flagNames = append(flagNames, f.Name)
				})
				sort.Strings(flagNames)
				expectedFlags := []string{
					"kubeconfig",
					"kubeconfig-secret-name",
					"kubeconfig-secret-namespace",
					"oneshot",
					"service-account-name",
					"service-account-namespace",
					"token-audience",
					"token-file",
				}
				g.Expect(flagNames).To(Equal(expectedFlags))
			},
		},
		{
			name: "When token-minter command is created, it should default service-account-namespace to kube-system",
			test: func(t *testing.T, g Gomega) {
				cmd := NewStartCommand()
				f := cmd.Flag("service-account-namespace")
				g.Expect(f).NotTo(BeNil())
				g.Expect(f.DefValue).To(Equal("kube-system"))
			},
		},
		{
			name: "When token-minter command is created, it should default token-audience to openshift",
			test: func(t *testing.T, g Gomega) {
				cmd := NewStartCommand()
				f := cmd.Flag("token-audience")
				g.Expect(f).NotTo(BeNil())
				g.Expect(f.DefValue).To(Equal("openshift"))
			},
		},
		{
			name: "When token-minter command is created, it should default token-file to /var/run/secrets/openshift/serviceaccount/token",
			test: func(t *testing.T, g Gomega) {
				cmd := NewStartCommand()
				f := cmd.Flag("token-file")
				g.Expect(f).NotTo(BeNil())
				g.Expect(f.DefValue).To(Equal("/var/run/secrets/openshift/serviceaccount/token"))
			},
		},
		{
			name: "When token-minter command is created, it should default kubeconfig to /etc/kubernetes/kubeconfig",
			test: func(t *testing.T, g Gomega) {
				cmd := NewStartCommand()
				f := cmd.Flag("kubeconfig")
				g.Expect(f).NotTo(BeNil())
				g.Expect(f.DefValue).To(Equal("/etc/kubernetes/kubeconfig"))
			},
		},
		{
			name: "When token-minter command is created, it should mark service-account-namespace as required",
			test: func(t *testing.T, g Gomega) {
				cmd := NewStartCommand()
				f := cmd.Flag("service-account-namespace")
				g.Expect(f).NotTo(BeNil())
				g.Expect(f.Annotations).To(HaveKey("cobra_annotation_bash_completion_one_required_flag"))
			},
		},
		{
			name: "When token-minter command is created, it should mark service-account-name as required",
			test: func(t *testing.T, g Gomega) {
				cmd := NewStartCommand()
				f := cmd.Flag("service-account-name")
				g.Expect(f).NotTo(BeNil())
				g.Expect(f.Annotations).To(HaveKey("cobra_annotation_bash_completion_one_required_flag"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			tt.test(t, g)
		})
	}
}

func TestRenewDurationFromExpiration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, g Gomega)
	}{
		{
			name: "When expiration is 100 seconds from now, it should return approximately 80 seconds",
			test: func(t *testing.T, g Gomega) {
				expiration := metav1.NewTime(time.Now().Add(100 * time.Second))
				result := renewDurationFromExpiration(expiration)
				g.Expect(result).To(BeNumerically("~", 80*time.Second, 1*time.Second))
			},
		},
		{
			name: "When expiration is in the past, it should return a negative duration",
			test: func(t *testing.T, g Gomega) {
				expiration := metav1.NewTime(time.Now().Add(-100 * time.Second))
				result := renewDurationFromExpiration(expiration)
				g.Expect(result).To(BeNumerically("<", 0))
			},
		},
		{
			name: "When expiration is exactly now, it should return zero",
			test: func(t *testing.T, g Gomega) {
				expiration := metav1.NewTime(time.Now())
				result := renewDurationFromExpiration(expiration)
				g.Expect(result).To(BeNumerically("~", 0, 1*time.Second))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			tt.test(t, g)
		})
	}
}
