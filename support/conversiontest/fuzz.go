package conversiontest

import (
	"math/rand"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/go-cmp/cmp"
	fuzz "github.com/google/gofuzz"
	"github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/apitesting/fuzzer"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metafuzzer "k8s.io/apimachinery/pkg/apis/meta/fuzzer"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

// Source: https://github.com/kubernetes-sigs/cluster-api/blob/1ec0cd6174f1b860dc466db587241ea7edea0b9f/util/conversion/conversion.go

// GetFuzzer returns a new fuzzer to be used for testing.
func GetFuzzer(scheme *runtime.Scheme, funcs ...fuzzer.FuzzerFuncs) *fuzz.Fuzzer {
	funcs = append([]fuzzer.FuzzerFuncs{
		metafuzzer.Funcs,
		func(_ runtimeserializer.CodecFactory) []interface{} {
			return []interface{}{
				// Custom fuzzer for metav1.Time pointers which weren't
				// fuzzed and always resulted in `nil` values.
				// This implementation is somewhat similar to the one provided
				// in the metafuzzer.Funcs.
				func(input *metav1.Time, c fuzz.Continue) {
					if input != nil {
						var sec, nsec uint32
						c.Fuzz(&sec)
						c.Fuzz(&nsec)
						fuzzed := metav1.Unix(int64(sec), int64(nsec)).Rfc3339Copy()
						input.Time = fuzzed.Time
					}
				},
			}
		},
	}, funcs...)
	return fuzzer.FuzzerFor(
		fuzzer.MergeFuzzerFuncs(funcs...),
		rand.NewSource(rand.Int63()), //nolint:gosec
		runtimeserializer.NewCodecFactory(scheme),
	).NumElements(1, 5)
}

// FuzzTestFuncInput contains input parameters
// for the FuzzTestFunc function.
type FuzzTestFuncInput struct {
	Scheme *runtime.Scheme

	Hub              conversion.Hub
	HubAfterMutation func(conversion.Hub)

	Spoke                      conversion.Convertible
	SpokeAfterMutation         func(convertible conversion.Convertible)
	SkipSpokeAnnotationCleanup bool

	FuzzerFuncs []fuzzer.FuzzerFuncs
}

// FuzzTestFunc returns a new testing function to be used in tests to make sure conversions between
// the Hub version of an object and an older version aren't lossy.
func FuzzTestFunc(input FuzzTestFuncInput) func(*testing.T) {
	if input.Scheme == nil {
		input.Scheme = scheme.Scheme
	}

	return func(t *testing.T) {
		t.Helper()

		t.Run("spoke-hub-spoke", func(t *testing.T) {
			g := gomega.NewWithT(t)
			fuzzer := GetFuzzer(input.Scheme, input.FuzzerFuncs...)

			for i := 0; i < 100; i++ {
				// Create the spoke and fuzz it
				spokeBefore := input.Spoke.DeepCopyObject().(conversion.Convertible)
				fuzzer.Fuzz(spokeBefore)

				// First convert spoke to hub
				hubCopy := input.Hub.DeepCopyObject().(conversion.Hub)
				g.Expect(spokeBefore.ConvertTo(hubCopy)).To(gomega.Succeed())

				// Convert hub back to spoke and check if the resulting spoke is equal to the spoke before the round trip
				spokeAfter := input.Spoke.DeepCopyObject().(conversion.Convertible)
				g.Expect(spokeAfter.ConvertFrom(hubCopy)).To(gomega.Succeed())

				if input.SpokeAfterMutation != nil {
					input.SpokeAfterMutation(spokeAfter)
				}

				g.Expect(apiequality.Semantic.DeepEqual(spokeBefore, spokeAfter)).To(gomega.BeTrue(), cmp.Diff(spokeBefore, spokeAfter))
			}
		})

		t.Run("hub-spoke-hub", func(t *testing.T) {
			g := gomega.NewWithT(t)
			fuzzer := GetFuzzer(input.Scheme, input.FuzzerFuncs...)

			for i := 0; i < 100; i++ {
				// Create the hub and fuzz it
				hubBefore := input.Hub.DeepCopyObject().(conversion.Hub)
				fuzzer.Fuzz(hubBefore)

				// First convert hub to spoke
				dstCopy := input.Spoke.DeepCopyObject().(conversion.Convertible)
				g.Expect(dstCopy.ConvertFrom(hubBefore)).To(gomega.Succeed())

				// Convert spoke back to hub and check if the resulting hub is equal to the hub before the round trip
				hubAfter := input.Hub.DeepCopyObject().(conversion.Hub)
				g.Expect(dstCopy.ConvertTo(hubAfter)).To(gomega.Succeed())

				if input.HubAfterMutation != nil {
					input.HubAfterMutation(hubAfter)
				}
				g.Expect(apiequality.Semantic.DeepEqual(hubBefore, hubAfter)).To(gomega.BeTrue(), cmp.Diff(hubBefore, hubAfter))
			}
		})
	}
}
