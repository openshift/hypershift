package util

import (
	"os"

	dhparam "github.com/Luzifer/go-dhparam"
)

const (
	bitSize = 2048
)

func dhCallback(r dhparam.GeneratorResult) {
	switch r {
	case dhparam.GeneratorFoundPossiblePrime:
		os.Stdout.WriteString(".")
	case dhparam.GeneratorFirstConfirmation:
		os.Stdout.WriteString("+")
	case dhparam.GeneratorSafePrimeFound:
		os.Stdout.WriteString("*\n")
	}
}

func GenerateDHParams() ([]byte, error) {
	dh, err := dhparam.Generate(bitSize, dhparam.GeneratorTwo, dhCallback)
	if err != nil {
		return nil, err
	}
	pem, err := dh.ToPEM()
	if err != nil {
		return nil, err
	}
	return pem, nil
}
