package etcdcli

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"k8s.io/component-base/metrics/legacyregistry"
	klog "k8s.io/klog/v2"

	"github.com/prometheus/client_golang/prometheus"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func init() {
	legacyregistry.RawMustRegister(raftTerms)
}

const raftTermsMetricName = "etcd_debugging_raft_terms_total"

// raftTermsCollector is thread-safe internally
var raftTerms = &raftTermsCollector{
	desc: prometheus.NewDesc(
		raftTermsMetricName,
		"Number of etcd raft terms as observed by each member.",
		[]string{"member"},
		prometheus.Labels{},
	),
	terms: map[string]uint64{},
	lock:  sync.RWMutex{},
}

type healthCheck struct {
	Member  *etcdserverpb.Member
	Healthy bool
	Took    string
	Error   error
}

type memberHealth []healthCheck

func getMemberHealth(ctx context.Context, etcdMembers []*etcdserverpb.Member) memberHealth {
	memberHealth := memberHealth{}
	for _, member := range etcdMembers {
		if !HasStarted(member) {
			memberHealth = append(memberHealth, healthCheck{Member: member, Healthy: false})
			continue
		}

		const defaultTimeout = 30 * time.Second
		resChan := make(chan healthCheck, 1)
		go func() {
			ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
			defer cancel()

			resChan <- checkSingleMemberHealth(ctx, member)
		}()

		select {
		case res := <-resChan:
			memberHealth = append(memberHealth, res)
		case <-time.After(defaultTimeout):
			memberHealth = append(memberHealth, healthCheck{
				Member:  member,
				Healthy: false,
				Error: fmt.Errorf("30s timeout waiting for member %s to respond to health check",
					member.Name)})
		}

		close(resChan)
	}

	// Purge any unknown members from the raft term metrics collector.
	for _, cachedMember := range raftTerms.List() {
		found := false
		for _, member := range etcdMembers {
			if member.Name == cachedMember {
				found = true
				break
			}
		}

		if !found {
			// Forget is a map deletion underneath, which is idempotent and under a lock.
			raftTerms.Forget(cachedMember)
		}
	}

	return memberHealth
}

func checkSingleMemberHealth(ctx context.Context, member *etcdserverpb.Member) healthCheck {
	// If the endpoint is for a learner member then we should skip testing the connection
	// via the member list call as learners don't support that.
	// The learner's connection would get tested in the health check below
	skipConnectionTest := member.IsLearner

	cli, err := newEtcdClientWithClientOpts([]string{member.ClientURLs[0]}, skipConnectionTest)
	if err != nil {
		return healthCheck{
			Member:  member,
			Healthy: false,
			Error:   fmt.Errorf("create client failure: %w", err)}
	}

	defer func() {
		if err := cli.Close(); err != nil {
			klog.Errorf("error closing etcd client for getMemberHealth: %v", err)
		}
	}()

	st := time.Now()

	var resp *clientv3.GetResponse
	if member.IsLearner {
		// Learner members only support serializable (without consensus) read requests
		resp, err = cli.Get(ctx, "health", clientv3.WithSerializable())
	} else {
		// Linearized request to verify health of a voting member
		resp, err = cli.Get(ctx, "health")
	}

	hc := healthCheck{Member: member, Healthy: false, Took: time.Since(st).String()}
	if err == nil {
		if resp.Header != nil {
			// TODO(thomas): this is a somewhat misplaced side-effect that is safe to call from multiple goroutines
			raftTerms.Set(member.Name, resp.Header.RaftTerm)
		}
		hc.Healthy = true
	} else {
		klog.Errorf("health check for member (%v) failed: err(%v)", member.Name, err)
		hc.Error = fmt.Errorf("health check failed: %w", err)
	}

	return hc
}

// Status returns a reporting of memberHealth status
func (h memberHealth) Status() string {
	healthyMembers := h.GetHealthyMembers()

	status := []string{}
	if len(h) == len(healthyMembers) {
		status = append(status, fmt.Sprintf("%d members are available", len(h)))
	} else {
		status = append(status, fmt.Sprintf("%d of %d members are available", len(healthyMembers), len(h)))
		for _, etcd := range h {
			switch {
			case !HasStarted(etcd.Member):
				status = append(status, fmt.Sprintf("%s has not started", GetMemberNameOrHost(etcd.Member)))
			case !etcd.Healthy:
				status = append(status, fmt.Sprintf("%s is unhealthy", etcd.Member.Name))
			}
		}
	}
	return strings.Join(status, ", ")
}

// GetHealthyMembers returns healthy members
func (h memberHealth) GetHealthyMembers() []*etcdserverpb.Member {
	members := []*etcdserverpb.Member{}
	for _, etcd := range h {
		if etcd.Healthy {
			members = append(members, etcd.Member)
		}
	}
	return members
}

// GetUnhealthy returns unhealthy members
func (h memberHealth) GetUnhealthyMembers() []*etcdserverpb.Member {
	members := []*etcdserverpb.Member{}
	for _, etcd := range h {
		if !etcd.Healthy {
			members = append(members, etcd.Member)
		}
	}
	return members
}

// GetUnstarted returns unstarted members
func (h memberHealth) GetUnstartedMembers() []*etcdserverpb.Member {
	members := []*etcdserverpb.Member{}
	for _, etcd := range h {
		if !HasStarted(etcd.Member) {
			members = append(members, etcd.Member)
		}
	}
	return members
}

// GetUnhealthyMemberNames returns a list of unhealthy member names
func GetUnhealthyMemberNames(memberHealth []healthCheck) []string {
	memberNames := []string{}
	for _, etcd := range memberHealth {
		if !etcd.Healthy {
			memberNames = append(memberNames, GetMemberNameOrHost(etcd.Member))
		}
	}
	return memberNames
}

// GetHealthyMemberNames returns a list of healthy member names
func GetHealthyMemberNames(memberHealth []healthCheck) []string {
	memberNames := []string{}
	for _, etcd := range memberHealth {
		if etcd.Healthy {
			memberNames = append(memberNames, etcd.Member.Name)
		}
	}
	return memberNames
}

// GetUnstartedMemberNames returns a list of unstarted member names
func GetUnstartedMemberNames(memberHealth []healthCheck) []string {
	memberNames := []string{}
	for _, etcd := range memberHealth {
		if !HasStarted(etcd.Member) {
			memberNames = append(memberNames, GetMemberNameOrHost(etcd.Member))
		}
	}
	return memberNames
}

// HasStarted return true if etcd member has started.
func HasStarted(member *etcdserverpb.Member) bool {
	return len(member.ClientURLs) != 0
}

// IsQuorumFaultTolerant checks the current etcd cluster and returns true if the cluster can tolerate the
// loss of a single etcd member. Such loss is common during new static pod revision.
func IsQuorumFaultTolerant(memberHealth []healthCheck) bool {
	totalMembers := len(memberHealth)
	quorum, err := MinimumTolerableQuorum(totalMembers)
	if err != nil {
		klog.Errorf("etcd cluster could not determine minimum quorum required. total number of members is %v. minimum quorum required is %v: %s", totalMembers, quorum, err)
		return false
	}
	healthyMembers := len(GetHealthyMemberNames(memberHealth))
	switch {
	case totalMembers-quorum < 1:
		klog.Errorf("etcd cluster has quorum of %d which is not fault tolerant: %+v", quorum, memberHealth)
		return false
	case healthyMembers-quorum < 1:
		klog.Errorf("etcd cluster has quorum of %d and %d healthy members which is not fault tolerant: %+v", quorum, healthyMembers, memberHealth)
		return false
	}
	return true
}

// IsQuorumFaultTolerantErr is the same as IsQuorumFaultTolerant but with an error return instead of the log
func IsQuorumFaultTolerantErr(memberHealth []healthCheck) error {
	totalMembers := len(memberHealth)
	quorum, err := MinimumTolerableQuorum(totalMembers)
	if err != nil {
		return fmt.Errorf("etcd cluster could not determine minimum quorum required. total number of members is %v. minimum quorum required is %v: %w", totalMembers, quorum, err)
	}
	healthyMembers := len(GetHealthyMemberNames(memberHealth))
	switch {
	case totalMembers-quorum < 1:
		return fmt.Errorf("etcd cluster has quorum of %d which is not fault tolerant: %+v", quorum, memberHealth)
	case healthyMembers-quorum < 1:
		return fmt.Errorf("etcd cluster has quorum of %d and %d healthy members which is not fault tolerant: %+v", quorum, healthyMembers, memberHealth)
	}
	return nil
}

func IsClusterHealthy(memberHealth memberHealth) bool {
	unhealthyMembers := memberHealth.GetUnhealthyMembers()
	return len(unhealthyMembers) == 0
}

// raftTermsCollector is a Prometheus collector to re-expose raft terms as a counter.
type raftTermsCollector struct {
	desc  *prometheus.Desc
	terms map[string]uint64
	lock  sync.RWMutex
}

func (c *raftTermsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *raftTermsCollector) Set(member string, value uint64) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.terms[member] = value
}

func (c *raftTermsCollector) Forget(member string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.terms, member)
}

func (c *raftTermsCollector) List() []string {
	c.lock.RLock()
	defer c.lock.RUnlock()
	var members []string
	for member := range c.terms {
		members = append(members, member)
	}
	return members
}

func (c *raftTermsCollector) Collect(ch chan<- prometheus.Metric) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for member, val := range c.terms {
		ch <- prometheus.MustNewConstMetric(
			c.desc,
			prometheus.CounterValue,
			float64(val),
			member,
		)
	}
}

func MinimumTolerableQuorum(members int) (int, error) {
	if members <= 0 {
		return 0, fmt.Errorf("invalid etcd member length: %v", members)
	}
	return (members / 2) + 1, nil
}
