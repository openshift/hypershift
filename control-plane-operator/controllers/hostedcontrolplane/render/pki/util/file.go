package util

import (
	"os"
)

func FileExists(fileName string) bool {
	if _, err := os.Stat(fileName); err != nil {
		return false
	}
	return true
}

func CertExists(fileName string) bool {
	return FileExists(fileName + ".crt")
}

func CertAndKeyExists(fileName string) bool {
	return FileExists(fileName+".crt") && FileExists(fileName+".key")
}

func KubeconfigExists(fileName string) bool {
	return FileExists(fileName + ".kubeconfig")
}
