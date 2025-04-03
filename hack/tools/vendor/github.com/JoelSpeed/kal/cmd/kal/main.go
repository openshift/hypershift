package main

import (
	kalanalysis "github.com/JoelSpeed/kal/pkg/analysis"
	"github.com/JoelSpeed/kal/pkg/config"
	"golang.org/x/tools/go/analysis/multichecker"
)

func main() {
	analyzers, err := kalanalysis.NewRegistry().InitializeLinters(config.Linters{}, config.LintersConfig{})
	if err != nil {
		panic(err)
	}

	multichecker.Main(
		analyzers...,
	)
}
