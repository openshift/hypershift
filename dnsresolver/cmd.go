package dnsresolver

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resolve-dns NAME",
		Short: "Utility that ensures a DNS name can be resolved.",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				fmt.Printf("Specify a DNS name to lookup\n")
			}
			if err := resolveDNS(context.Background(), args[0]); err != nil {
				fmt.Printf("Error: %v", err)
				os.Exit(1)
			}
		},
	}
	return cmd
}

func resolveDNS(ctx context.Context, hostName string) error {
	err := wait.PollUntilContextTimeout(ctx, time.Second, 10*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, hostName)
		if err == nil && len(ips) > 0 {
			fmt.Printf("Address %s resolved to %s\n", hostName, ips[0].String())
			return true, nil
		}
		fmt.Printf("Address %s not resolved yet: %v\n", hostName, err)
		return false, nil
	})
	if err != nil {
		fmt.Printf("failed to resolve DNS name, giving up\n")
		return err
	}
	return nil
}
