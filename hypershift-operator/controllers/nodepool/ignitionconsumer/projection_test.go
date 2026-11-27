package ignitionconsumer

import (
	"context"
	"sort"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func refNames(refs []hyperv1.ConfigMapReference) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name)
	}
	sort.Strings(out)
	return out
}

var (
	testUserConfigs = []corev1.ConfigMap{
		{ObjectMeta: metav1.ObjectMeta{Name: "user-mc", Namespace: "clusters"}, Data: map[string]string{configDataKey: "user-data"}},
	}
	testCoreConfigs = []corev1.ConfigMap{
		{ObjectMeta: metav1.ObjectMeta{Name: "core-cfg", Namespace: "hcp"}, Data: map[string]string{configDataKey: "core-data"}},
	}
	testNTOConfigs = []corev1.ConfigMap{
		{ObjectMeta: metav1.ObjectMeta{Name: "nto-cfg", Namespace: "hcp"}, Data: map[string]string{configDataKey: "nto-data"}},
	}
)

// TestClassifyConfigs pins the classification (RF#1) and that it is pure (no cluster
// writes): user configs are classified rollout under their copy name; core and NTO under
// their own names; HAProxy is mgmt; rolloutGlobalConfig is returned separately (a dedicated
// spec field, not a rolloutConfigMaps entry).
func TestClassifyConfigs(t *testing.T) {
	g := NewWithT(t)

	userCopy := userConfigCopyName("np-1", "user-mc")
	haproxyName := haproxyConfigMapName("np-1")

	rolloutRefs, mgmtRefs, globalName := classifyConfigs("np-1", testUserConfigs, testCoreConfigs, testNTOConfigs)

	g.Expect(refNames(rolloutRefs)).To(Equal([]string{"core-cfg", "nto-cfg", userCopy}))
	g.Expect(refNames(mgmtRefs)).To(Equal([]string{haproxyName}))
	g.Expect(globalName).To(Equal(rolloutGlobalConfigMapName("np-1")))
	g.Expect(refNames(rolloutRefs)).ToNot(ContainElement(globalName))
}

// TestUserConfigCopyName pins that copied user ConfigMaps live in their own naming domain, so a user
// ConfigMap named like a generated one neither lands in both reference lists (rejected by the API)
// nor gets overwritten by the generated content, and that every copy name is a valid ConfigMap name,
// shortened only when it does not fit.
func TestUserConfigCopyName(t *testing.T) {
	testCases := []struct {
		name       string
		sourceName string
		// expectedName, when set, is the exact copy name.
		expectedName string
	}{
		{name: "When a user ConfigMap name fits, it should not be shortened", sourceName: "user-mc", expectedName: "user-user-mc-np-1"},
		{name: "When a user ConfigMap is named apiserver-haproxy, it should not collide with the HAProxy ConfigMap", sourceName: "apiserver-haproxy"},
		{name: "When a user ConfigMap is named rollout-global-config, it should not collide with the rolloutGlobalConfig ConfigMap", sourceName: "rollout-global-config"},
		{
			name:       "When a long dotted user ConfigMap name is shortened right after a dot, it should still be a valid name",
			sourceName: strings.Repeat("a.", 117) + "bbbbbbbbbb",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			userConfigs := []corev1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: tc.sourceName, Namespace: "clusters"}}}

			copyName := userConfigCopyName("np-1", tc.sourceName)
			g.Expect(validation.IsDNS1123Subdomain(copyName)).To(BeEmpty(), "copy name %q", copyName)
			if tc.expectedName != "" {
				g.Expect(copyName).To(Equal(tc.expectedName))
			}
			g.Expect(copyName).ToNot(Equal(haproxyConfigMapName("np-1")))
			g.Expect(copyName).ToNot(Equal(rolloutGlobalConfigMapName("np-1")))

			rolloutRefs, mgmtRefs, globalName := classifyConfigs("np-1", userConfigs, testCoreConfigs, testNTOConfigs)
			for _, ref := range mgmtRefs {
				g.Expect(refNames(rolloutRefs)).ToNot(ContainElement(ref.Name))
			}
			g.Expect(refNames(rolloutRefs)).ToNot(ContainElement(globalName))
		})
	}
}

// TestMaterializeConfigs pins the copy-vs-reference-in-place rule (RF#2): user configs,
// HAProxy, and rolloutGlobalConfig are created CR-owned in the HCP namespace; core and NTO
// are referenced in place and must NOT be copied/owned.
func TestMaterializeConfigs(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	const hcpNS = "hcp"

	owner := &hyperv1.IgnitionPayload{
		ObjectMeta: metav1.ObjectMeta{Name: "np-1", Namespace: hcpNS, UID: "uid-1"},
	}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()

	g.Expect(materializeConfigs(ctx, c, hcpNS, owner, testUserConfigs, "haproxy-raw", []byte("global-bytes"))).To(Succeed())

	assertOwnedCM := func(name, wantData string) {
		cm := &corev1.ConfigMap{}
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: name}, cm)).To(Succeed())
		g.Expect(cm.Data[configDataKey]).To(Equal(wantData))
		g.Expect(cm.OwnerReferences).To(HaveLen(1))
		g.Expect(cm.OwnerReferences[0].Name).To(Equal("np-1"))
		g.Expect(cm.OwnerReferences[0].Controller).ToNot(BeNil())
		g.Expect(*cm.OwnerReferences[0].Controller).To(BeTrue())
	}

	assertOwnedCM(userConfigCopyName("np-1", "user-mc"), "user-data")
	assertOwnedCM(haproxyConfigMapName("np-1"), "haproxy-raw")
	assertOwnedCM(rolloutGlobalConfigMapName("np-1"), "global-bytes")

	// (RF#2) core and NTO are referenced in place: materializeConfigs must not create them.
	notFound := func(name string) {
		cm := &corev1.ConfigMap{}
		g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: name}, cm)).To(HaveOccurred())
	}
	notFound("core-cfg")
	notFound("nto-cfg")
}
