package main

import (
	"context"

	"github.com/openshift/hypershift/contrib/gomaxprocs-webhook/cmd"
)

func main() {
	ctx := context.Background()
	cmd.Execute(ctx)
}

