package etcddefrag

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/go-logr/logr"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"k8s.io/apimachinery/pkg/util/wait"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/openshift/hypershift/pkg/etcdcli"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/library-go/pkg/operator/events"
)

const (
	pollWaitDuration                       = 2 * time.Second
	pollTimeoutDuration                    = 60 * time.Second
	maxDefragFailuresBeforeDegrade         = 3
	minDefragBytes                 int64   = 100 * 1024 * 1024 // 100MB
	minDefragWaitDuration                  = 36 * time.Second
	maxFragmentedPercentage        float64 = 45

	controllerRequeueDuration = 10 * time.Minute
)

type DefragController struct {
	client.Client
	log logr.Logger

	ControllerName string
	upsert.CreateOrUpdateProvider

	etcdClient         etcdcli.EtcdClient
	numDefragFailures  int
	defragWaitDuration time.Duration
}

type defragTicker struct {
	defrag *DefragController
}

func (r *DefragController) setupTicker(mgr manager.Manager) error {
	ticker := defragTicker{
		defrag: r,
	}
	if err := mgr.Add(&ticker); err != nil {
		return fmt.Errorf("failed to add defrag ticker runnable to manager: %w", err)
	}
	return nil
}

func (m *defragTicker) Start(ctx context.Context) error {
	ticker := time.NewTicker(controllerRequeueDuration)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.defrag.log.Info("Running defrag.")
			if err := m.defrag.runDefrag(ctx); err != nil {
				m.defrag.log.Error(err, "failed to run defragmentation cycle")
			}
		}
	}
}

func (r *DefragController) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	endpointsFunc := func() ([]string, error) {
		return r.etcdEndpoints(ctx)
	}
	r.etcdClient = etcdcli.NewEtcdClient(endpointsFunc, events.NewLoggingEventRecorder(r.ControllerName))

	// Set this so that it will immediately requeue itself.
	r.defragWaitDuration = minDefragWaitDuration

	if err := r.setupTicker(mgr); err != nil {
		return fmt.Errorf("failed to set up ticker: %w", err)
	}

	return nil
}

func (r *DefragController) etcdEndpoints(ctx context.Context) ([]string, error) {
	var eplist []string

	// Because we are part of the etcd pod, we can just use localhost.
	// The client itself will discover the other endpoints.
	eplist = append(eplist, "https://localhost:2379")
	return eplist, nil
}

/*

Everything from here down is from the cluster-etcd-controller code.
It's been modified mostly to replace 'c' with 'r' as the object name.
Also the logging has been changed.

https://github.com/openshift/cluster-etcd-operator/blob/master/pkg/operator/defragcontroller/defragcontroller.go

https://github.com/openshift/cluster-etcd-operator/tree/master/pkg/etcdcli

*/

func (r *DefragController) runDefrag(ctx context.Context) error {
	// Do not defrag if any of the cluster members are unhealthy.
	members, err := r.etcdClient.MemberList(ctx)
	if err != nil {
		return err
	}
	r.log.Info("Checking status for Defrag", "members", members)
	for _, m := range members {
		status, err := r.etcdClient.Status(ctx, m.ClientURLs[0])
		if err != nil {
			r.log.Error(err, "Member returned error", "member", m)
		} else {
			fragmentedPercentage := checkFragmentationPercentage(status.DbSize, status.DbSizeInUse)
			r.log.Info("Member", "name", m.Name, "URL", m.ClientURLs[0], "fragmentation percentage", fragmentedPercentage, "DBSize on disk", status.DbSize, "DBSize in use", status.DbSizeInUse, "leader", status.Leader)
		}
	}

	memberHealth, err := r.etcdClient.MemberHealth(ctx)
	if err != nil {
		return err
	}

	if !etcdcli.IsClusterHealthy(memberHealth) {
		r.log.Error(err, "Cluster is unhealthy", "status", memberHealth.Status())
		return fmt.Errorf("cluster is unhealthy, status: %s", memberHealth.Status())
	}

	// filter out learner members since they don't support the defragment API call
	var etcdMembers []*etcdserverpb.Member
	for _, m := range members {
		if !m.IsLearner {
			etcdMembers = append(etcdMembers, m)
		}
	}

	var endpointStatus []*clientv3.StatusResponse
	var leader *clientv3.StatusResponse
	for _, member := range etcdMembers {
		if len(member.ClientURLs) == 0 {
			// skip unstarted member
			continue
		}
		status, err := r.etcdClient.Status(ctx, member.ClientURLs[0])
		if err != nil {
			return err
		}
		if leader == nil && status.Leader == member.ID {
			leader = status
			continue
		}
		endpointStatus = append(endpointStatus, status)
	}

	// Leader last if possible.
	if leader != nil {
		r.log.Info("Appending leader last", "ID", leader.Header.MemberId)
		endpointStatus = append(endpointStatus, leader)
	}

	successfulDefrags := 0
	var errs []error
	for _, status := range endpointStatus {
		member, err := getMemberFromStatus(etcdMembers, status)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		// Check each member's status which includes the db size on disk "DbSize" and the db size in use "DbSizeInUse"
		// compare the % difference and if that difference is over the max diff threshold and also above the minimum
		// db size we defrag the members state file. In the case where this command only partially completed controller
		// can clean that up on the next sync. Having the db sizes slightly different is not a problem in itself.
		if r.isEndpointBackendFragmented(member, status) {
			fragmentedPercentage := checkFragmentationPercentage(status.DbSize, status.DbSizeInUse)
			r.log.Info("Member is over defrag threshold", "name", member.Name, "URL", member.ClientURLs[0], "fragmentation percentage", fragmentedPercentage, "DBSize on disk", status.DbSize, "DBSize in use", status.DbSizeInUse, "leader", status.Leader)
			if _, err := r.etcdClient.Defragment(ctx, member); err != nil {
				// Defrag can timeout if defragmentation takes longer than etcdcli.DefragDialTimeout.
				r.log.Error(err, "DefragController Defragment Failed", "member", member.Name, "ID", member.ID)
				errs = append(errs, fmt.Errorf("failed defrag on member: %s, memberID: %x: %v", member.Name, member.ID, err))
				continue
			}

			r.log.Info("DefragController Defragment Success", "member", member.Name, "ID", member.ID)
			successfulDefrags++

			// Give cluster time to recover before we move to the next member.
			if err := wait.PollUntilContextTimeout(
				ctx,
				pollWaitDuration,
				pollTimeoutDuration,
				true,
				func(ctx context.Context) (bool, error) {
					// Ensure defragmentation attempts have clear observable signal.
					r.log.Info("Sleeping to allow cluster to recover before defragging next member", "waiting", r.defragWaitDuration)
					time.Sleep(r.defragWaitDuration)

					memberHealth, err := r.etcdClient.MemberHealth(ctx)
					if err != nil {
						r.log.Error(err, "Failed checking member health")
						return false, nil
					}
					if !etcdcli.IsClusterHealthy(memberHealth) {
						r.log.Info("Cluster member is unhealthy", "member status", memberHealth.Status())
						return false, nil
					}
					return true, nil
				}); err != nil {
				errs = append(errs, fmt.Errorf("timeout waiting for cluster to stabilize after defrag: %w", err))
			}
		} else {
			// no fragmentation needed is also a success
			successfulDefrags++
		}
	}

	if successfulDefrags != len(endpointStatus) {
		r.numDefragFailures++
		r.log.Info("DefragController Defragment Partial Failure", "successfully defragged", successfulDefrags, "of members", len(endpointStatus), "tries remaining", maxDefragFailuresBeforeDegrade-r.numDefragFailures)

		// TODO: This should bubble up to HCP condition errors.
		return errors.Join(errs...)
	}

	if len(errs) > 0 {
		r.log.Info("found errors even though all members have been successfully defragmented", "error", errors.Join(errs...))
	}

	return nil
}

// isEndpointBackendFragmented checks the status of all cluster members to ensure that no members have a fragmented store.
// This can happen if the operator starts defrag of the cluster but then loses leader status and is rescheduled before
// the operator can defrag all members.
func (r *DefragController) isEndpointBackendFragmented(member *etcdserverpb.Member, endpointStatus *clientv3.StatusResponse) bool {
	if endpointStatus == nil {
		r.log.Error(nil, "endpoint status validation failed", "status", endpointStatus)
		return false
	}
	fragmentedPercentage := checkFragmentationPercentage(endpointStatus.DbSize, endpointStatus.DbSizeInUse)

	r.log.Info("Etcd member backend store fragmentation status", "name", member.Name, "URL", member.ClientURLs[0], "fragmentation percentage", fragmentedPercentage, "DBSize on disk", endpointStatus.DbSize, "DBSize in use", endpointStatus.DbSizeInUse)

	return fragmentedPercentage >= maxFragmentedPercentage && endpointStatus.DbSize >= minDefragBytes
}

func checkFragmentationPercentage(ondisk, inuse int64) float64 {
	diff := float64(ondisk - inuse)
	fragmentedPercentage := (diff / float64(ondisk)) * 100
	return math.Round(fragmentedPercentage*100) / 100
}

func getMemberFromStatus(members []*etcdserverpb.Member, endpointStatus *clientv3.StatusResponse) (*etcdserverpb.Member, error) {
	if endpointStatus == nil {
		return nil, fmt.Errorf("endpoint status validation failed: %v", endpointStatus)
	}
	for _, member := range members {
		if member.ID == endpointStatus.Header.MemberId {
			return member, nil
		}
	}
	return nil, fmt.Errorf("no member found in MemberList matching ID: %v", endpointStatus.Header.MemberId)
}
