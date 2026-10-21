package main

import (
	"github.com/openshift/hypershift/api"
	copied "github.com/openshift/hypershift/support/thirdparty"
)

type worker interface{ Work() }
type implementation struct{}

func (implementation) Work() { ViaInterface() }
func ViaInterface()          {}
func Callback()              {}
func ViaVendor()             {}
func ViaCopied()             {}
func ViaGenerated()          {}
func TestOnly()              {}
func TaggedOnly()            {}
func DeadExported()          {}

func invoke(w worker, f func()) { w.Work(); f() }

func main() {
	invoke(implementation{}, Callback)
	api.Invoke(ViaVendor)
	copied.Invoke(ViaCopied)
}
