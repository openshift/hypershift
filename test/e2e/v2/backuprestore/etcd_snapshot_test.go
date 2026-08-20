//go:build e2ev2 && backuprestore

package backuprestore

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

func TestParseEtcdInitLogs(t *testing.T) {
	tests := []struct {
		name             string
		logs             string
		restoreStarted   bool
		restoreCompleted bool
		restoreSkipped   bool
		lineCount        int
	}{
		{
			name: "When etcd-init logs show successful snapshot restore it should detect both restoring and restored",
			logs: `INFO: using etcdutl (etcd 3.6+)
+----------+----------+------------+------------+---------+
|   HASH   | REVISION | TOTAL KEYS | TOTAL SIZE | VERSION |
+----------+----------+------------+------------+---------+
| 5643d825 |   578454 |       3209 |      49 MB |   3.6.0 |
+----------+----------+------------+------------+---------+
2026-04-13T07:33:20Z    info    snapshot/v3_snapshot.go:305     restoring snapshot      {"path": "/tmp/snapshot"}
2026-04-13T07:33:20Z    info    membership/cluster.go:424       added member
2026-04-13T07:33:20Z    info    snapshot/v3_snapshot.go:333     restored snapshot       {"path": "/tmp/snapshot"}`,
			restoreStarted:   true,
			restoreCompleted: true,
			restoreSkipped:   false,
			lineCount:        9,
		},
		{
			name: "When data directory is not empty it should detect restore was skipped",
			logs: `/var/lib/data not empty, not restoring snapshot`,
			restoreStarted:   false,
			restoreCompleted: false,
			restoreSkipped:   true,
			lineCount:        1,
		},
		{
			name:             "When logs contain only curl progress it should detect neither restore started nor completed",
			logs:             `  % Total    % Received % Xferd  Average Speed   Time    Time     Time  Current`,
			restoreStarted:   false,
			restoreCompleted: false,
			restoreSkipped:   false,
			lineCount:        1,
		},
		{
			name: "When restore starts but does not complete it should detect only restoring",
			logs: `INFO: using etcdutl (etcd 3.6+)
2026-04-13T07:33:20Z    info    snapshot/v3_snapshot.go:305     restoring snapshot      {"path": "/tmp/snapshot"}`,
			restoreStarted:   true,
			restoreCompleted: false,
			restoreSkipped:   false,
			lineCount:        2,
		},
		{
			name:             "When logs are empty it should detect nothing",
			logs:             ``,
			restoreStarted:   false,
			restoreCompleted: false,
			restoreSkipped:   false,
			lineCount:        0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			reader := strings.NewReader(tt.logs)
			result, err := parseEtcdInitLogs(reader)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result.restoreStarted).To(Equal(tt.restoreStarted), "restoreStarted mismatch")
			g.Expect(result.restoreCompleted).To(Equal(tt.restoreCompleted), "restoreCompleted mismatch")
			g.Expect(result.restoreSkipped).To(Equal(tt.restoreSkipped), "restoreSkipped mismatch")
			g.Expect(result.lineCount).To(Equal(tt.lineCount), "lineCount mismatch")
		})
	}
}

func TestParseEtcdMemberList(t *testing.T) {
	tests := []struct {
		name            string
		jsonOutput      string
		expectedMembers int
		expectedErr     bool
	}{
		{
			name: "When etcd member list returns a healthy 3-member cluster it should parse all members",
			jsonOutput: `{"header":{"cluster_id":12345678901234567},"members":[` +
				`{"ID":1,"name":"etcd-0","peerURLs":["https://etcd-0.etcd-discovery.ns.svc:2380"],"clientURLs":["https://etcd-0.etcd-discovery.ns.svc:2379"]},` +
				`{"ID":2,"name":"etcd-1","peerURLs":["https://etcd-1.etcd-discovery.ns.svc:2380"],"clientURLs":["https://etcd-1.etcd-discovery.ns.svc:2379"]},` +
				`{"ID":3,"name":"etcd-2","peerURLs":["https://etcd-2.etcd-discovery.ns.svc:2380"],"clientURLs":["https://etcd-2.etcd-discovery.ns.svc:2379"]}]}`,
			expectedMembers: 3,
			expectedErr:     false,
		},
		{
			name: "When etcd member list returns a single member it should parse one member (possible split brain)",
			jsonOutput: `{"header":{"cluster_id":99999},"members":[` +
				`{"ID":1,"name":"etcd-0","peerURLs":["https://etcd-0.etcd-discovery.ns.svc:2380"],"clientURLs":["https://etcd-0.etcd-discovery.ns.svc:2379"]}]}`,
			expectedMembers: 1,
			expectedErr:     false,
		},
		{
			name:            "When etcd member list returns empty JSON object it should parse zero members",
			jsonOutput:      `{"header":{"cluster_id":0},"members":[]}`,
			expectedMembers: 0,
			expectedErr:     false,
		},
		{
			name:            "When etcd member list returns invalid JSON it should return an error",
			jsonOutput:      `not json`,
			expectedMembers: 0,
			expectedErr:     true,
		},
		{
			name: "When etcd member list returns a member without a name it should parse it with empty name",
			jsonOutput: `{"header":{"cluster_id":12345},"members":[` +
				`{"ID":1,"name":"","peerURLs":["https://etcd-0.etcd-discovery.ns.svc:2380"],"clientURLs":[]}]}`,
			expectedMembers: 1,
			expectedErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := parseEtcdMemberList(tt.jsonOutput)
			if tt.expectedErr {
				g.Expect(err).To(HaveOccurred(), "expected parsing error")
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result.Members).To(HaveLen(tt.expectedMembers), "member count mismatch")
		})
	}
}

func TestMatchesHCPEtcdBackupName(t *testing.T) {
	tests := []struct {
		name               string
		hcpEtcdBackupName  string
		oadpBackupName     string
		expectedMatch      bool
	}{
		{
			name:              "When HCPEtcdBackup name matches the oadp pattern it should return true",
			hcpEtcdBackupName: "oadp-mycluster-mynamespace-abc123-xyz78",
			oadpBackupName:    "mycluster-mynamespace-abc123",
			expectedMatch:     true,
		},
		{
			name:              "When HCPEtcdBackup name does not match it should return false",
			hcpEtcdBackupName: "some-other-backup",
			oadpBackupName:    "mycluster-mynamespace-abc123",
			expectedMatch:     false,
		},
		{
			name:              "When HCPEtcdBackup name is the exact backup name without prefix it should return false",
			hcpEtcdBackupName: "mycluster-mynamespace-abc123",
			oadpBackupName:    "mycluster-mynamespace-abc123",
			expectedMatch:     false,
		},
		{
			name:              "When HCPEtcdBackup name only shares a backup name prefix it should return false",
			hcpEtcdBackupName: "oadp-mycluster-mynamespace-abc1234-xyz78",
			oadpBackupName:    "mycluster-mynamespace-abc123",
			expectedMatch:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(MatchesHCPEtcdBackupName(tt.hcpEtcdBackupName, tt.oadpBackupName)).To(Equal(tt.expectedMatch))
		})
	}
}
