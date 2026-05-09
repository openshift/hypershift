package bannedimport

import (
	_ "github.com/google/go-containerregistry/pkg/v1/remote" // want "direct import of registry/release package"
)
