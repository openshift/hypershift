//go:build e2e

package e2e

import (
	"embed"
)

//go:embed assets/*
var content embed.FS
