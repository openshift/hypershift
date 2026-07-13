package oadp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

// GetOptions holds common configuration for get commands.
type GetOptions struct {
	OADPNamespace string
	HCName        string
	HCNamespace   string
	Output        string
	Log           logr.Logger
	Client        client.Client
}

func NewGetBackupsCommand() *cobra.Command {
	opts := &GetOptions{Log: log.Log}
	cmd := &cobra.Command{
		Use:   "oadp-backups",
		Short: "List OADP backups for hosted clusters",
		Long: `List Velero backup resources in the OADP namespace.

Examples:
  # List all backups
  hypershift get oadp-backups

  # Filter by hosted cluster
  hypershift get oadp-backups --hc-name example --hc-namespace clusters

  # Output as JSON
  hypershift get oadp-backups -o json`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runGet(cmd.Context(), "Backup")
		},
	}
	addGetFlags(cmd, opts)
	return cmd
}

func NewGetRestoresCommand() *cobra.Command {
	opts := &GetOptions{Log: log.Log}
	cmd := &cobra.Command{
		Use:   "oadp-restores",
		Short: "List OADP restores for hosted clusters",
		Long: `List Velero restore resources in the OADP namespace.

Examples:
  # List all restores
  hypershift get oadp-restores

  # Filter by hosted cluster
  hypershift get oadp-restores --hc-name example --hc-namespace clusters`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runGet(cmd.Context(), "Restore")
		},
	}
	addGetFlags(cmd, opts)
	return cmd
}

func NewGetSchedulesCommand() *cobra.Command {
	opts := &GetOptions{Log: log.Log}
	cmd := &cobra.Command{
		Use:   "oadp-schedules",
		Short: "List OADP schedules for hosted clusters",
		Long: `List Velero schedule resources in the OADP namespace.

Examples:
  # List all schedules
  hypershift get oadp-schedules

  # Filter by hosted cluster
  hypershift get oadp-schedules --hc-name example --hc-namespace clusters`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runGet(cmd.Context(), "Schedule")
		},
	}
	addGetFlags(cmd, opts)
	return cmd
}

func addGetFlags(cmd *cobra.Command, opts *GetOptions) {
	cmd.Flags().StringVar(&opts.OADPNamespace, "oadp-namespace", "openshift-adp", "Namespace where OADP operator is installed")
	cmd.Flags().StringVar(&opts.HCName, "hc-name", "", "Filter by hosted cluster name")
	cmd.Flags().StringVar(&opts.HCNamespace, "hc-namespace", "", "Filter by hosted cluster namespace")
	cmd.Flags().StringVarP(&opts.Output, "output", "o", "table", "Output format: table, json, yaml")
}

func (o *GetOptions) runGet(ctx context.Context, kind string) error {
	if o.Client == nil {
		var err error
		o.Client, err = util.GetClient()
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}
	}

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    kind + "List",
	})

	if err := o.Client.List(ctx, list, client.InNamespace(o.OADPNamespace)); err != nil {
		return fmt.Errorf("failed to list %s resources: %w", kind, err)
	}

	items := o.filterItems(list.Items)

	sort.Slice(items, func(i, j int) bool {
		ti := items[i].GetCreationTimestamp()
		tj := items[j].GetCreationTimestamp()
		return ti.After(tj.Time)
	})

	switch strings.ToLower(o.Output) {
	case "json":
		return outputJSON(os.Stdout, items)
	case "yaml":
		return outputYAML(os.Stdout, items)
	default:
		return outputTable(os.Stdout, items, kind)
	}
}

func (o *GetOptions) filterItems(items []unstructured.Unstructured) []unstructured.Unstructured {
	if o.HCName == "" && o.HCNamespace == "" {
		return items
	}

	prefix := ""
	if o.HCName != "" && o.HCNamespace != "" {
		prefix = fmt.Sprintf("%s-%s", o.HCName, o.HCNamespace)
	}

	var filtered []unstructured.Unstructured
	for _, item := range items {
		name := item.GetName()
		labels := item.GetLabels()

		if matchesByLabel(labels, o.HCName, o.HCNamespace) || (prefix != "" && strings.HasPrefix(name, prefix)) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func matchesByLabel(labels map[string]string, hcName, hcNamespace string) bool {
	if labels == nil {
		return false
	}
	if hcName != "" {
		if v, ok := labels["hypershift.openshift.io/hosted-cluster"]; ok && v == hcName {
			if hcNamespace == "" {
				return true
			}
			if v2, ok2 := labels["hypershift.openshift.io/hosted-cluster-namespace"]; ok2 && v2 == hcNamespace {
				return true
			}
		}
	}
	return false
}

func outputTable(w io.Writer, items []unstructured.Unstructured, kind string) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)

	switch kind {
	case "Schedule":
		fmt.Fprintln(tw, "NAME\tSTATUS\tSCHEDULE\tLAST BACKUP\tAGE")
	default:
		fmt.Fprintln(tw, "NAME\tSTATUS\tAGE")
	}

	for _, item := range items {
		name := item.GetName()
		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		if phase == "" {
			phase = "Unknown"
		}
		age := formatAge(item.GetCreationTimestamp().Time)

		switch kind {
		case "Schedule":
			schedule, _, _ := unstructured.NestedString(item.Object, "spec", "schedule")
			lastBackup, _, _ := unstructured.NestedString(item.Object, "status", "lastBackup")
			lastBackupAge := ""
			if lastBackup != "" {
				if t, err := time.Parse(time.RFC3339, lastBackup); err == nil {
					lastBackupAge = formatAge(t)
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", name, phase, schedule, lastBackupAge, age)
		default:
			fmt.Fprintf(tw, "%s\t%s\t%s\n", name, phase, age)
		}
	}

	return tw.Flush()
}

func outputJSON(w io.Writer, items []unstructured.Unstructured) error {
	objs := make([]map[string]interface{}, len(items))
	for i, item := range items {
		objs[i] = item.Object
	}
	data, err := json.MarshalIndent(objs, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w)
	return err
}

func outputYAML(w io.Writer, items []unstructured.Unstructured) error {
	for i, item := range items {
		if i > 0 {
			fmt.Fprintln(w, "---")
		}
		data, err := yaml.Marshal(item.Object)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
