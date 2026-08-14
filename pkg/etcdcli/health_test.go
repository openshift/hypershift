package etcdcli

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
)

func TestMemberHealthStatus(t *testing.T) {
	tests := []struct {
		name         string
		memberHealth memberHealth
		want         string
	}{
		{
			"test all available members",
			[]healthCheck{
				healthyMember(1),
				healthyMember(2),
				healthyMember(3),
			},
			"3 members are available",
		},
		{
			"test an unhealthy member",
			[]healthCheck{
				healthyMember(1),
				healthyMember(2),
				unHealthyMember(3),
			},
			"2 of 3 members are available, etcd-3 is unhealthy",
		},
		{
			"test an unstarted member",
			[]healthCheck{
				healthyMember(1),
				healthyMember(2),
				unstartedMember(3),
			},
			"2 of 3 members are available, NAME-PENDING-10.0.0.3 has not started",
		},
		{
			"test an unstarted member and an unhealthy member",
			[]healthCheck{
				healthyMember(1),
				unHealthyMember(2),
				unstartedMember(3),
			},
			"1 of 3 members are available, etcd-2 is unhealthy, NAME-PENDING-10.0.0.3 has not started",
		},
		{
			"test two unhealthy members",
			[]healthCheck{
				healthyMember(1),
				unHealthyMember(2),
				unHealthyMember(3),
			},
			"1 of 3 members are available, etcd-2 is unhealthy, etcd-3 is unhealthy",
		},
		{
			"test two unstarted members",
			[]healthCheck{
				healthyMember(1),
				unstartedMember(2),
				unstartedMember(3),
			},
			"1 of 3 members are available, NAME-PENDING-10.0.0.2 has not started, NAME-PENDING-10.0.0.3 has not started",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.memberHealth.Status(); got != tt.want {
				t.Errorf("test %q = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestGetUnstartedMemberNames(t *testing.T) {
	tests := []struct {
		name         string
		memberHealth memberHealth
		want         []string
	}{
		{
			"test all available members",
			[]healthCheck{
				healthyMember(1),
				healthyMember(2),
				healthyMember(3),
			},
			[]string{},
		},
		{
			"test an unhealthy members",
			[]healthCheck{
				healthyMember(1),
				healthyMember(2),
				unHealthyMember(3),
			},
			[]string{},
		},
		{
			"test an unstarted and an unhealthy member",
			[]healthCheck{
				unHealthyMember(1),
				unstartedMember(2),
				healthyMember(3),
			},
			[]string{"NAME-PENDING-10.0.0.2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetUnstartedMemberNames(tt.memberHealth)
			if !reflect.DeepEqual(tt.want, got) {
				t.Errorf("test %q = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestGetUnhealthyMemberNames(t *testing.T) {
	tests := []struct {
		name         string
		memberHealth memberHealth
		want         []string
	}{
		{
			"test all available members",
			[]healthCheck{
				healthyMember(1),
				healthyMember(2),
				healthyMember(3),
			},
			[]string{},
		},
		{
			"test an unhealthy members",
			[]healthCheck{
				healthyMember(1),
				healthyMember(2),
				unHealthyMember(3),
			},
			[]string{"etcd-3"},
		},
		{
			"test an unstarted member",
			[]healthCheck{
				healthyMember(1),
				unstartedMember(2),
				healthyMember(3),
			},
			[]string{"NAME-PENDING-10.0.0.2"},
		},
		{
			"test an unstarted and an unhealthy member",
			[]healthCheck{
				unHealthyMember(1),
				unstartedMember(2),
				healthyMember(3),
			},
			[]string{"etcd-1", "NAME-PENDING-10.0.0.2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetUnhealthyMemberNames(tt.memberHealth)
			if !reflect.DeepEqual(tt.want, got) {
				t.Errorf("test %q = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestIsQuorumFaultTolerant(t *testing.T) {
	tests := []struct {
		name         string
		memberHealth memberHealth
		want         bool
	}{
		{
			"test all available members",
			[]healthCheck{
				healthyMember(1),
				healthyMember(2),
				healthyMember(3),
			},
			true,
		},
		{
			"test an unhealthy members",
			[]healthCheck{
				healthyMember(1),
				healthyMember(2),
				unHealthyMember(3),
			},
			false,
		},
		{
			"test an unstarted member",
			[]healthCheck{
				healthyMember(1),
				unstartedMember(2),
				healthyMember(3),
			},
			false,
		},
		{
			"test an unstarted and an unhealthy member",
			[]healthCheck{
				unHealthyMember(1),
				unstartedMember(2),
				healthyMember(3),
			},
			false,
		},
		{
			"test etcd cluster with less than 3 members",
			[]healthCheck{
				healthyMember(1),
				healthyMember(2),
			},
			false,
		},
		{
			"test empty health check",
			[]healthCheck{},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsQuorumFaultTolerant(tt.memberHealth)
			if got != tt.want {
				t.Errorf("test %q = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func unstartedMember(member int) healthCheck {
	return healthCheck{
		Member: &etcdserverpb.Member{
			PeerURLs: []string{fmt.Sprintf("https://10.0.0.%d:2380", member)},
		},
		Healthy: false,
	}
}
func healthyMember(member int) healthCheck {
	return healthCheck{
		Member: &etcdserverpb.Member{
			Name:       fmt.Sprintf("etcd-%d", member),
			PeerURLs:   []string{fmt.Sprintf("https://10.0.0.%d:2380", member)},
			ClientURLs: []string{fmt.Sprintf("https://10.0.0.%d:2379", member)},
		},
		Healthy: true,
	}
}

func TestMinimumTolerableQuorum(t *testing.T) {

	scenarios := []struct {
		name   string
		input  int
		expErr error
		exp    int
	}{
		{
			name:   "valid input `3`",
			input:  3,
			expErr: nil,
			exp:    2,
		},
		{
			name:   "valid input `5`",
			input:  5,
			expErr: nil,
			exp:    3,
		},
		{
			name:   "invalid input `0`",
			input:  0,
			expErr: fmt.Errorf("invalid etcd member length: %v", 0),
			exp:    0,
		},
		{
			name:   "invalid input `-10`",
			input:  -10,
			expErr: fmt.Errorf("invalid etcd member length: %v", -10),
			exp:    0,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			// act
			actual, err := MinimumTolerableQuorum(scenario.input)
			// assert
			require.Equal(t, scenario.expErr, err)
			require.Equal(t, scenario.exp, actual)
		})
	}
}

func unHealthyMember(member int) healthCheck {
	return healthCheck{
		Member: &etcdserverpb.Member{
			Name:       fmt.Sprintf("etcd-%d", member),
			PeerURLs:   []string{fmt.Sprintf("https://10.0.0.%d:2380", member)},
			ClientURLs: []string{fmt.Sprintf("https://10.0.0.%d:2379", member)},
		},
		Healthy: false,
	}
}
