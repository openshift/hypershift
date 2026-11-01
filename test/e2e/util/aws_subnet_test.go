package util

import "testing"

func TestFindFreeSubnetCIDR(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		vpcs, occupied []string
		want           string
	}{
		{name: "When a custom VPC has occupied subnets, it should select a disjoint CIDR", vpcs: []string{"10.42.0.0/24"}, occupied: []string{"10.42.0.0/27"}, want: "10.42.0.32/28"},
		{name: "When the first VPC range is full, it should use another associated range", vpcs: []string{"10.42.0.0/28", "10.43.0.0/28"}, occupied: []string{"10.42.0.0/28"}, want: "10.43.0.0/28"},
		{name: "When all addresses are occupied, it should fail", vpcs: []string{"10.42.0.0/28"}, occupied: []string{"10.42.0.0/28"}},
		{name: "When the VPC is IPv6 only, it should fail", vpcs: []string{"2001:db8::/56"}},
		{name: "When VPC CIDRs are absent, it should fail"},
		{name: "When a VPC CIDR is malformed, it should fail", vpcs: []string{"bad"}},
		{name: "When an occupied CIDR is malformed, it should fail", vpcs: []string{"10.42.0.0/24"}, occupied: []string{"bad"}},
		{name: "When a VPC range is too large, it should fail", vpcs: []string{"10.0.0.0/8"}},
		{name: "When the last IPv4 block is full, it should terminate without wrapping", vpcs: []string{"255.255.255.240/28"}, occupied: []string{"255.255.255.240/28"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := FindFreeSubnetCIDR(testCase.vpcs, testCase.occupied)
			if actual != testCase.want || (err != nil) != (testCase.want == "") {
				t.Fatalf("got %q, %v; want %q", actual, err, testCase.want)
			}
		})
	}
}
