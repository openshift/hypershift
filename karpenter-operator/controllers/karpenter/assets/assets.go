package assets

import (
	"embed"
)

const DefaultKarpenterProviderAWSImage = "public.ecr.aws/karpenter/controller:1.2.3"

//go:embed *.yaml
var f embed.FS

// ReadFile reads and returns the content of the named file.
func ReadFile(name string) ([]byte, error) {
	return f.ReadFile(name)
}
