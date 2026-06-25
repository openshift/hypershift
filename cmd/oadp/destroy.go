package oadp

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

// DestroyOptions holds common configuration for destroy commands.
type DestroyOptions struct {
	Name          string
	OADPNamespace string
	Log           logr.Logger
	Client        client.Client
}

func NewDestroyBackupCommand() *cobra.Command {
	opts := &DestroyOptions{Log: log.Log}
	cmd := &cobra.Command{
		Use:   "oadp-backup",
		Short: "Delete an OADP backup",
		Long: `Delete a Velero backup resource from the OADP namespace.

Examples:
  # Delete a specific backup
  hypershift destroy oadp-backup --name example-clusters-lkbtzw`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runDestroy(cmd.Context(), "Backup")
		},
	}
	addDestroyFlags(cmd, opts)
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func NewDestroyRestoreCommand() *cobra.Command {
	opts := &DestroyOptions{Log: log.Log}
	cmd := &cobra.Command{
		Use:   "oadp-restore",
		Short: "Delete an OADP restore",
		Long: `Delete a Velero restore resource from the OADP namespace.

Examples:
  # Delete a specific restore
  hypershift destroy oadp-restore --name restore-example-clusters-abc123`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runDestroy(cmd.Context(), "Restore")
		},
	}
	addDestroyFlags(cmd, opts)
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func NewDestroyScheduleCommand() *cobra.Command {
	opts := &DestroyOptions{Log: log.Log}
	cmd := &cobra.Command{
		Use:   "oadp-schedule",
		Short: "Delete an OADP schedule",
		Long: `Delete a Velero schedule resource from the OADP namespace.

Examples:
  # Delete a specific schedule
  hypershift destroy oadp-schedule --name example-clusters-daily`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runDestroy(cmd.Context(), "Schedule")
		},
	}
	addDestroyFlags(cmd, opts)
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func addDestroyFlags(cmd *cobra.Command, opts *DestroyOptions) {
	cmd.Flags().StringVar(&opts.Name, "name", "", "Name of the resource to delete (required)")
	cmd.Flags().StringVar(&opts.OADPNamespace, "oadp-namespace", "openshift-adp", "Namespace where OADP operator is installed")
}

func (o *DestroyOptions) runDestroy(ctx context.Context, kind string) error {
	if o.Client == nil {
		var err error
		o.Client, err = util.GetClient()
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}
	}

	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("velero.io/v1")
	obj.SetKind(kind)

	key := client.ObjectKey{Name: o.Name, Namespace: o.OADPNamespace}
	if err := o.Client.Get(ctx, key, obj); err != nil {
		return fmt.Errorf("%s '%s' not found in namespace '%s': %w", kind, o.Name, o.OADPNamespace, err)
	}

	if err := o.Client.Delete(ctx, obj); err != nil {
		return fmt.Errorf("failed to delete %s '%s': %w", kind, o.Name, err)
	}

	o.Log.Info(fmt.Sprintf("%s deleted successfully", kind), "name", o.Name, "namespace", o.OADPNamespace)
	return nil
}
