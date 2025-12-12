package main

import (
	"context"

	"github.com/openshift/hypershift/contrib/oadp-recovery/cmd"
)

func main() {
	ctx := context.Background()
	cmd.Execute(ctx)
}