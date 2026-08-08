package etcd

// etcdRaftQuorumSize returns the minimum number of voting members that must be
// present for a Raft quorum for a cluster of clusterSize members.
func etcdRaftQuorumSize(clusterSize int) int {
	if clusterSize <= 0 {
		return 0
	}
	return clusterSize/2 + 1
}

// resetMemberStragglerJoinQuorumMet returns true when the number of members
// reported by etcdctl member list is already sufficient for the cluster to have
// raft quorum for an expected cluster of expectedClusterSize, and the cluster
// is not already at the expected size (so dynamic member add is appropriate).
func resetMemberStragglerJoinQuorumMet(memberCount, expectedClusterSize int) bool {
	if expectedClusterSize <= 0 {
		return false
	}
	return memberCount >= etcdRaftQuorumSize(expectedClusterSize) && memberCount < expectedClusterSize
}
