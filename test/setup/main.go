package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io/ioutil"
	"os"
	"text/template"
	"time"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var log = ctrl.Log.WithName("e2e-setup")

//go:embed cluster-monitoring-config.yaml
var clusterMonitoringConfigTemplateString string
var clusterMonitoringConfigTemplate = template.Must(template.New("config").Parse(clusterMonitoringConfigTemplateString))

//go:embed user-workload-monitoring-config.yaml
var userWorkloadMonitoringConfigTemplateString string
var userWorkloadMonitoringConfigTemplate = template.Must(template.New("config").Parse(userWorkloadMonitoringConfigTemplateString))

func main() {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Provides test setup commands",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
			os.Exit(1)
		},
	}
	cmd.AddCommand(monitoringCommand())

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

type MonitoringOptions struct {
	RemoteWriteURL string

	RemoteWriteUsername string
	RemoteWritePassword string

	RemoteWriteUsernameFile string
	RemoteWritePasswordFile string

	ProwJobID string
}

func monitoringCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "monitoring",
		Short: "Configures a management cluster for e2e monitoring integration",
	}

	opts := &MonitoringOptions{ProwJobID: os.Getenv("PROW_JOB_ID")}

	cmd.Flags().StringVar(&opts.RemoteWriteURL, "remote-write-url", opts.RemoteWriteURL, "Remote write URL. If specified, configures monitoring for remote write.")
	cmd.Flags().StringVar(&opts.RemoteWriteUsername, "remote-write-username", opts.RemoteWriteUsername, "Remote write username")
	cmd.Flags().StringVar(&opts.RemoteWritePassword, "remote-write-password", opts.RemoteWritePassword, "Remote write password")
	cmd.Flags().StringVar(&opts.RemoteWriteUsernameFile, "remote-write-username-file", opts.RemoteWriteUsernameFile, "Remote write username file")
	cmd.Flags().StringVar(&opts.RemoteWritePasswordFile, "remote-write-password-file", opts.RemoteWritePasswordFile, "Remote write password file")
	cmd.Flags().StringVar(&opts.ProwJobID, "prow-job-id", opts.ProwJobID, "The ProwJobID. If set, it will be added as a static label to the remote_write config.")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
		ctx := ctrl.SetupSignalHandler()

		cl, err := e2eutil.GetClient()
		if err != nil {
			log.Error(err, "failed to get k8s client")
			os.Exit(1)
		}
		if err := opts.Configure(ctx, cl); err != nil {
			log.Error(err, "failed to configure monitoring")
			os.Exit(1)
		}
	}

	return cmd
}

func (o *MonitoringOptions) Configure(ctx context.Context, k client.Client) error {
	var clusterMonitoringConfigYAML bytes.Buffer
	if err := clusterMonitoringConfigTemplate.Execute(&clusterMonitoringConfigYAML, o); err != nil {
		return err
	}
	var userWorkloadMonitoringConfigYAML bytes.Buffer
	if err := userWorkloadMonitoringConfigTemplate.Execute(&userWorkloadMonitoringConfigYAML, o); err != nil {
		return err
	}

	// Collect remote write config if specified
	var username, password string
	if len(o.RemoteWriteURL) > 0 {
		log.Info("remote write will be enabled")
		username = o.RemoteWriteUsername
		if len(o.RemoteWriteUsernameFile) > 0 {
			u, err := ioutil.ReadFile(o.RemoteWriteUsernameFile)
			if err != nil {
				return err
			}
			username = string(u)
		}
		password = o.RemoteWritePassword
		if len(o.RemoteWritePasswordFile) > 0 {
			p, err := ioutil.ReadFile(o.RemoteWritePasswordFile)
			if err != nil {
				return err
			}
			password = string(p)
		}
		if len(username) == 0 {
			return fmt.Errorf("username is required")
		}
		if len(password) == 0 {
			return fmt.Errorf("password is required")
		}
	}

	// Install the remote write secret referenced by the remote write configuration
	clusterMonitoringRemoteWriteSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-monitoring",
			Name:      "remote-write-creds",
		},
	}
	if result, err := controllerutil.CreateOrUpdate(ctx, k, clusterMonitoringRemoteWriteSecret, func() error {
		clusterMonitoringRemoteWriteSecret.Data = map[string][]byte{
			"username": []byte(username),
			"password": []byte(password),
		}
		return nil
	}); err != nil {
		return err
	} else {
		log.Info("updated cluster monitoring remote write secret", "result", result)
	}

	// Enable user workload monitoring and remote write from the cluster monitoring
	// stack
	clusterMonitoringConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-monitoring",
			Name:      "cluster-monitoring-config",
		},
	}
	if result, err := controllerutil.CreateOrUpdate(ctx, k, clusterMonitoringConfig, func() error {
		clusterMonitoringConfig.Data = map[string]string{
			"config.yaml": clusterMonitoringConfigYAML.String(),
		}
		return nil
	}); err != nil {
		return err
	} else {
		log.Info("updated cluster monitoring config", "result", result)
	}

	// Wait for the user workload namespace to exist, which is a loose indicator
	// that user workload monitoring was enabled
	userWorkloadMonitoringNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-user-workload-monitoring",
		},
	}
	err := wait.PollUntil(1*time.Second, func() (done bool, err error) {
		err = k.Get(ctx, client.ObjectKeyFromObject(userWorkloadMonitoringNamespace), userWorkloadMonitoringNamespace)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("waiting for user workload monitoring namespace to exist", "namespace", userWorkloadMonitoringNamespace.Name)
				return false, nil
			}
			return false, err
		}
		return true, nil
	}, ctx.Done())
	if err != nil {
		return fmt.Errorf("failed waiting for user workload monitoring namespace")
	}

	// Install the remote write secret referenced by the remote write configuration
	userWorkloadMonitoringRemoteWriteSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-user-workload-monitoring",
			Name:      "remote-write-creds",
		},
	}
	if result, err := controllerutil.CreateOrUpdate(ctx, k, userWorkloadMonitoringRemoteWriteSecret, func() error {
		userWorkloadMonitoringRemoteWriteSecret.Data = map[string][]byte{
			"username": []byte(username),
			"password": []byte(password),
		}
		return nil
	}); err != nil {
		return err
	} else {
		log.Info("updated user workload monitoring remote write secret", "result", result)
	}

	// Configure user workload monitoring for remote write
	userWorkloadMonitoringConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-user-workload-monitoring",
			Name:      "user-workload-monitoring-config",
		},
	}
	if result, err := controllerutil.CreateOrUpdate(ctx, k, userWorkloadMonitoringConfig, func() error {
		userWorkloadMonitoringConfig.Data = map[string]string{
			"config.yaml": userWorkloadMonitoringConfigYAML.String(),
		}
		return nil
	}); err != nil {
		return err
	} else {
		log.Info("updated user workload monitoring config", "result", result)
	}

	return nil
}
