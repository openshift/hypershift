package awsutil

// ShouldIgnoreRevokePermissionError returns true when revoking an ingress or
// egress rule fails because the permission has already been removed.
// This avoids treating an already-revoked rule as a hard error.
func ShouldIgnoreRevokePermissionError(code string) bool {
	return code == InvalidPermissionNotFound
}

// ShouldIgnoreSGNotFound returns true when a security group deletion fails
// because the group was already deleted. This handles the race between
// DescribeSecurityGroups and DeleteSecurityGroup.
func ShouldIgnoreSGNotFound(code string) bool {
	return code == InvalidGroupNotFound
}
