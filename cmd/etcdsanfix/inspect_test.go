package etcdsanfix

import (
	"crypto/x509"
	"fmt"
	"testing"
)

func perMemberSANs(namespace string, replicas int) []string {
	var names []string
	for i := 0; i < replicas; i++ {
		names = append(names,
			fmt.Sprintf("etcd-%d.etcd-discovery.%s.svc", i, namespace),
			fmt.Sprintf("etcd-%d.etcd-discovery.%s.svc.cluster.local", i, namespace),
		)
	}
	return names
}

func wildcardSANs(namespace string) []string {
	return []string{
		fmt.Sprintf("*.etcd-discovery.%s.svc", namespace),
		fmt.Sprintf("*.etcd-discovery.%s.svc.cluster.local", namespace),
	}
}

func TestClassifyCertVerdict(t *testing.T) {
	const ns = "ns"
	tests := []struct {
		name        string
		dnsNames    []string
		replicas    int32
		wantStatus  string
		wantMissing int
	}{
		{
			name:        "wildcard only is the bug",
			dnsNames:    wildcardSANs(ns),
			replicas:    3,
			wantStatus:  "WILDCARD-ONLY",
			wantMissing: 6,
		},
		{
			name:        "wildcards plus all per-member is bridged",
			dnsNames:    append(wildcardSANs(ns), perMemberSANs(ns, 3)...),
			replicas:    3,
			wantStatus:  "BRIDGED",
			wantMissing: 0,
		},
		{
			name:        "wildcards plus only etcd-0 is partial",
			dnsNames:    append(wildcardSANs(ns), perMemberSANs(ns, 1)...),
			replicas:    3,
			wantStatus:  "PARTIAL",
			wantMissing: 4,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cert := &x509.Certificate{DNSNames: tc.dnsNames}
			rep := classifyCert(cert, ns, tc.replicas)
			if len(rep.missingPerMember) != tc.wantMissing {
				t.Errorf("missingPerMember = %d (%v), want %d", len(rep.missingPerMember), rep.missingPerMember, tc.wantMissing)
			}
			status, _ := certVerdict(rep)
			if status != tc.wantStatus {
				t.Errorf("verdict = %q, want %q", status, tc.wantStatus)
			}
		})
	}
}
