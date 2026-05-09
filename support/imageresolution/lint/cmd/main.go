package main

import (
	"github.com/openshift/hypershift/support/imageresolution/lint"

	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(lint.Analyzer)
}
