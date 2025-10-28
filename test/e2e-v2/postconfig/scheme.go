package postconfig

import (
	"sync"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	_scheme     *runtime.Scheme
	_schemeOnce sync.Once
)

func scheme() *runtime.Scheme {
	_schemeOnce.Do(func() {
		_scheme = runtime.NewScheme()
		_ = clientgoscheme.AddToScheme(_scheme)
		_ = hyperv1.AddToScheme(_scheme)
	})
	return _scheme
}
