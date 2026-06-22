package manifests

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestEtcdBackupJobManifests(t *testing.T) {
	tests := []struct {
		name     string
		testFunc func(g Gomega)
	}{
		{
			name: "When creating an EtcdBackupJob it should set the correct name and namespace with HCP labels",
			testFunc: func(g Gomega) {
				job := EtcdBackupJob("ho-namespace", "my-hcp")
				g.Expect(job.Name).To(Equal("etcd-backup-my-hcp"))
				g.Expect(job.Namespace).To(Equal("ho-namespace"))
				g.Expect(job.Labels).To(HaveKeyWithValue("app", "etcd-backup"))
				g.Expect(job.Labels).To(HaveKeyWithValue("hypershift.openshift.io/hcp", "my-hcp"))
			},
		},
		{
			name: "When creating an EtcdBackupJobServiceAccount it should use the correct name and namespace",
			testFunc: func(g Gomega) {
				sa := EtcdBackupJobServiceAccount("ho-namespace")
				g.Expect(sa.Name).To(Equal("etcd-backup-job"))
				g.Expect(sa.Namespace).To(Equal("ho-namespace"))
			},
		},
		{
			name: "When creating an EtcdBackupJobRole it should target the HCP namespace",
			testFunc: func(g Gomega) {
				role := EtcdBackupJobRole("clusters-my-hcp")
				g.Expect(role.Name).To(Equal("etcd-backup-job"))
				g.Expect(role.Namespace).To(Equal("clusters-my-hcp"))
			},
		},
		{
			name: "When creating an EtcdBackupJobRoleBinding it should target the HCP namespace",
			testFunc: func(g Gomega) {
				rb := EtcdBackupJobRoleBinding("clusters-my-hcp")
				g.Expect(rb.Name).To(Equal("etcd-backup-job"))
				g.Expect(rb.Namespace).To(Equal("clusters-my-hcp"))
			},
		},
		{
			name: "When creating an EtcdBackupNetworkPolicy it should target the HCP namespace",
			testFunc: func(g Gomega) {
				np := EtcdBackupNetworkPolicy("clusters-my-hcp")
				g.Expect(np.Name).To(Equal("allow-etcd-backup"))
				g.Expect(np.Namespace).To(Equal("clusters-my-hcp"))
			},
		},
		{
			name: "When creating the legacy EtcdBackupServiceAccount it should remain unchanged",
			testFunc: func(g Gomega) {
				sa := EtcdBackupServiceAccount("hcp-ns")
				g.Expect(sa.Name).To(Equal("etcd-backup-sa"))
				g.Expect(sa.Namespace).To(Equal("hcp-ns"))
			},
		},
		{
			name: "When creating the legacy EtcdBackupCronJob it should remain unchanged",
			testFunc: func(g Gomega) {
				cj := EtcdBackupCronJob("hcp-ns")
				g.Expect(cj.Name).To(Equal("etcd-backup"))
				g.Expect(cj.Namespace).To(Equal("hcp-ns"))
				g.Expect(cj.Spec.JobTemplate.Spec.Template.Labels).To(HaveKeyWithValue("app", "etcd-backup"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			tc.testFunc(g)
		})
	}
}
