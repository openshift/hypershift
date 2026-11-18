package etcdsanfix

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	etcdclient "go.etcd.io/etcd/client/v3"
)

func newHealthCommand() *cobra.Command {
	opts := newClientOptions()
	cmd := &cobra.Command{
		Use:          "health",
		Short:        "Show etcd member list, per-endpoint health, leader, and alarms",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return opts.printHealth(ctx, os.Stdout)
		},
	}
	addNamespaceFlag(cmd, opts)
	addEtcdClientFlags(cmd, opts)
	return cmd
}

// memberHealth is the per-member view the health and check subcommands share.
type memberHealth struct {
	id        uint64
	name      string
	isLearner bool
	started   bool
	healthy   bool
	status    *etcdclient.StatusResponse
}

// gatherHealth returns one memberHealth per registered member plus the cluster alarms.
// Each started member is dialed once (reused for the health probe and Status), so
// out-of-cluster this is one port-forward per member. Unreachable members yield nil status.
func (o *options) gatherHealth(ctx context.Context) ([]memberHealth, []*etcdserverpb.AlarmMember, error) {
	members, err := o.memberList(ctx)
	if err != nil {
		return nil, nil, err
	}
	hs := make([]memberHealth, 0, len(members))
	for _, m := range members {
		mh := memberHealth{id: m.ID, name: m.Name, isLearner: m.IsLearner, started: m.Name != ""}
		if m.Name != "" {
			if cli, endpoint, cleanup, derr := o.etcdDial(ctx, m.Name); derr == nil {
				mh.healthy = clientHealthy(ctx, cli)
				sctx, cancel := context.WithTimeout(ctx, etcdClientTimeout)
				if st, serr := cli.Status(sctx, endpoint); serr == nil {
					mh.status = st
				}
				cancel()
				cleanup()
			}
		}
		hs = append(hs, mh)
	}
	alarms, _ := o.alarmList(ctx)
	return hs, alarms, nil
}

func (o *options) printHealth(ctx context.Context, out io.Writer) error {
	fmt.Fprintf(out, "mode: %s\n", o.modeLabel())
	hs, alarms, err := o.gatherHealth(ctx)
	if err != nil {
		return fmt.Errorf("etcd member list failed: %w", err)
	}
	renderHealth(out, hs, alarms)
	return nil
}

func renderHealth(out io.Writer, hs []memberHealth, alarms []*etcdserverpb.AlarmMember) {
	fmt.Fprintf(out, "\n=== etcd health ===\n")
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MEMBER\tID\tROLE\tSTARTED\tHEALTHY\tLEADER\tDB SIZE\tVERSION")
	healthy, voters, learners := 0, 0, 0
	var leaderID uint64
	for _, mh := range hs {
		role := "voter"
		if mh.isLearner {
			role = "learner"
			learners++
		} else {
			voters++
		}
		name := mh.name
		if name == "" {
			name = "(unstarted)"
		}
		isLeader, db, version := "-", "-", "-"
		if mh.status != nil {
			if mh.status.Leader != 0 {
				leaderID = mh.status.Leader
				if mh.status.Header != nil && mh.status.Leader == mh.status.Header.MemberId {
					isLeader = "yes"
				}
			}
			db = humanBytes(mh.status.DbSize)
			if mh.status.Version != "" {
				version = mh.status.Version
			}
		}
		if mh.healthy {
			healthy++
		}
		fmt.Fprintf(tw, "%s\t%x\t%s\t%v\t%v\t%s\t%s\t%s\n", name, mh.id, role, mh.started, mh.healthy, isLeader, db, version)
	}
	tw.Flush()

	quorum := len(hs)/2 + 1
	fmt.Fprintf(out, "\nmembers=%d voters=%d learners=%d healthy=%d quorum=%d\n", len(hs), voters, learners, healthy, quorum)
	if leaderID == 0 {
		fmt.Fprintln(out, "leader: NONE (no quorum, or an election is in progress)")
	} else {
		fmt.Fprintf(out, "leader: %x\n", leaderID)
	}
	if healthy > quorum {
		fmt.Fprintf(out, "fault tolerance: can lose %d member(s) and keep quorum\n", healthy-quorum)
	} else {
		fmt.Fprintln(out, "fault tolerance: NONE (at bare quorum or below)")
	}
	if len(alarms) == 0 {
		fmt.Fprintln(out, "alarms: none")
	} else {
		fmt.Fprintf(out, "alarms: %d\n", len(alarms))
		for _, a := range alarms {
			fmt.Fprintf(out, "  member %x: %s\n", a.MemberID, a.Alarm.String())
		}
	}
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
