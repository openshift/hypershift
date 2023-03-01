package awslogforwarder

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/amazon-cloudwatch-agent/logs"
	"github.com/aws/amazon-cloudwatch-agent/plugins/outputs/cloudwatchlogs"
	"github.com/influxdata/telegraf/models"
	"github.com/influxdata/telegraf/plugins/outputs"
	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws-log-forwarder",
		Short: "Starts an audit log webhook server that sends log events to cloudwatch",
		Run: func(cmd *cobra.Command, args []string) {
			logger := models.NewLogger("cloudwatch", "forwarder", "")
			cfgCreator := outputs.Outputs["cloudwatchlogs"]
			cfg := cfgCreator().(*cloudwatchlogs.CloudWatchLogs)
			cfg.Filename = "/Users/cewong/.aws/credentials"
			cfg.Region = "us-east-2"
			cfg.LogStreamName = "kube-apiserver-audit"
			cfg.LogGroupName = "cewong-guest-audit-logs"
			cfg.RetentionInDays = 30
			cfg.ForceFlushInterval.Duration = 30 * time.Second
			cfg.Log = logger
			run(cfg)
		},
	}
	return cmd
}

func run(cfg *cloudwatchlogs.CloudWatchLogs) {
	dest := cfg.CreateDest("", "", -1)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "can't read body", http.StatusBadRequest)
			return
		}
		fmt.Printf("%s\n", string(body))
		dest.Publish([]logs.LogEvent{
			logLine(string(body)),
		})
	})
	fmt.Printf("Server listening on port 8080\n")
	panic(http.ListenAndServe(":8080", nil))
}

type logLine string

func (l logLine) Message() string {
	return string(l)
}

func (logLine) Time() time.Time {
	return time.Now()
}

func (logLine) Done() {
	// do nothing
}
