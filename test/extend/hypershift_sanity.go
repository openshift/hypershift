package extend

import (
	"context"
	"fmt"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
)

var _ = g.Describe("[sig-hypershift] Hypershift", func() {
	defer g.GinkgoRecover()
	g.BeforeEach(func(ctx context.Context) {
		fmt.Println("Prepare test environment...")
	})
	g.It("openshift-test-extension smoke test", func() {
		o.Expect(true).To(o.BeTrue())
	})
})
