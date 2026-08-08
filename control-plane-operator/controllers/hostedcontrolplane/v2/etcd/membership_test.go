package etcd

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestEtcdRaftQuorumSize(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		clusterSize   int
		expectedVotes int
	}{
		{
			name:          "When clusterSize is zero, it should return zero",
			clusterSize:   0,
			expectedVotes: 0,
		},
		{
			name:          "When clusterSize is negative, it should return zero",
			clusterSize:   -1,
			expectedVotes: 0,
		},
		{
			name:          "When clusterSize is one, it should return one",
			clusterSize:   1,
			expectedVotes: 1,
		},
		{
			name:          "When clusterSize is three, it should return two",
			clusterSize:   3,
			expectedVotes: 2,
		},
		{
			name:          "When clusterSize is five, it should return three",
			clusterSize:   5,
			expectedVotes: 3,
		},
		{
			name:          "When clusterSize is four, it should return three",
			clusterSize:   4,
			expectedVotes: 3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(etcdRaftQuorumSize(tc.clusterSize)).To(
				Equal(tc.expectedVotes),
				"Calculated quorum size for cluster size %d should be %d",
				tc.clusterSize, tc.expectedVotes)
		})
	}
}

func TestResetMemberStragglerJoinQuorumMet(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                string
		memberCount         int
		expectedClusterSz   int
		expectStragglerJoin bool
	}{
		{
			name:                "When expected cluster size is zero, it should return false",
			memberCount:         3,
			expectedClusterSz:   0,
			expectStragglerJoin: false,
		},
		{
			name:                "When member count is below quorum for three members, it should return false",
			memberCount:         1,
			expectedClusterSz:   3,
			expectStragglerJoin: false,
		},
		{
			name:                "When member count meets quorum for three members, it should return true",
			memberCount:         2,
			expectedClusterSz:   3,
			expectStragglerJoin: true,
		},
		{
			name:                "When member count is already at expected cluster size, it should return false",
			memberCount:         3,
			expectedClusterSz:   3,
			expectStragglerJoin: false,
		},
		{
			name:                "When member count is two below quorum for five members, it should return false",
			memberCount:         2,
			expectedClusterSz:   5,
			expectStragglerJoin: false,
		},
		{
			name:                "When member count meets quorum for five members, it should return true",
			memberCount:         3,
			expectedClusterSz:   5,
			expectStragglerJoin: true,
		},
		{
			name:                "When member count is at expected cluster size for five members, it should return false",
			memberCount:         5,
			expectedClusterSz:   5,
			expectStragglerJoin: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			got := resetMemberStragglerJoinQuorumMet(tc.memberCount, tc.expectedClusterSz)
			g.Expect(got).To(
				Equal(tc.expectStragglerJoin),
				"Expected straggler join for member count %d and expected cluster size %d should be %v",
				tc.memberCount, tc.expectedClusterSz, tc.expectStragglerJoin)
		})
	}
}
