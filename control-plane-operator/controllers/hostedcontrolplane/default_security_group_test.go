package hostedcontrolplane

import "testing"

func TestShouldRetryDefaultSecurityGroupDeletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		want bool
	}{
		{
			name: "When AWS returns dependency violation it should retry security group deletion",
			code: "DependencyViolation",
			want: true,
		},
		{
			name: "When AWS returns unauthorized operation it should not retry security group deletion",
			code: "UnauthorizedOperation",
			want: false,
		},
		{
			name: "When AWS returns another error code it should not retry security group deletion",
			code: "InvalidGroup.NotFound",
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldRetryDefaultSecurityGroupDeletion(tc.code)
			if got != tc.want {
				t.Fatalf("shouldRetryDefaultSecurityGroupDeletion(%q) = %t, want %t", tc.code, got, tc.want)
			}
		})
	}
}

func TestShouldRequeueDefaultSecurityGroupDeletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		code              string
		shouldRetryDelete bool
		want              bool
	}{
		{
			name:              "When AWS returns dependency violation it should requeue default security group deletion",
			code:              "DependencyViolation",
			shouldRetryDelete: false,
			want:              true,
		},
		{
			name:              "When delete is in progress it should requeue default security group deletion",
			code:              "",
			shouldRetryDelete: true,
			want:              true,
		},
		{
			name:              "When AWS returns unauthorized and delete is not in progress it should not requeue default security group deletion",
			code:              "UnauthorizedOperation",
			shouldRetryDelete: false,
			want:              false,
		},
		{
			name:              "When AWS returns another code and delete is not in progress it should not requeue default security group deletion",
			code:              "InvalidGroup.NotFound",
			shouldRetryDelete: false,
			want:              false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldRequeueDefaultSecurityGroupDeletion(tc.code, tc.shouldRetryDelete)
			if got != tc.want {
				t.Fatalf("shouldRequeueDefaultSecurityGroupDeletion(%q, %t) = %t, want %t", tc.code, tc.shouldRetryDelete, got, tc.want)
			}
		})
	}
}

func TestShouldIgnoreDefaultSecurityGroupRevokePermissionError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		want bool
	}{
		{
			name: "When revoke permission returns InvalidPermission.NotFound it should ignore the error",
			code: "InvalidPermission.NotFound",
			want: true,
		},
		{
			name: "When revoke permission returns another code it should not ignore the error",
			code: "UnauthorizedOperation",
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldIgnoreDefaultSecurityGroupRevokePermissionError(tc.code)
			if got != tc.want {
				t.Fatalf("shouldIgnoreDefaultSecurityGroupRevokePermissionError(%q) = %t, want %t", tc.code, got, tc.want)
			}
		})
	}
}
