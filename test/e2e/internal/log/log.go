package log

import (
	"fmt"

	. "github.com/onsi/ginkgo"
)

func Logf(format string, a ...interface{}) {
	fmt.Fprintf(GinkgoWriter, "INFO: "+format+"\n", a...)
}
