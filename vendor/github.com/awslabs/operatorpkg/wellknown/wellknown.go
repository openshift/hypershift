package wellknown

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/awslabs/operatorpkg/docs"
)

// Annotation is the source-of-truth description of a well known annotation.
// Declaring annotations as Annotations lets a documentation generator emit a
// reference page of every annotation an operator reads or writes, in the style of
// https://kubernetes.io/docs/reference/labels-annotations-taints/.
//
// Conventions:
//   - Describe every annotation an operator reads or writes with an Annotation.
//   - Values, when set, MUST be sourced from consts, never magic strings.
type Annotation struct {
	// Name is the annotation key, e.g. "karpenter.sh/do-not-disrupt".
	Name string
	// Example is a representative value, rendered as `<Name>: "<Example>"`.
	Example string
	// UsedOn lists the kinds of object the annotation is read from or written to,
	// e.g. &corev1.Node{}. Resolve an entry's kind with object.GVK.
	UsedOn []runtime.Object
	// Help is human-readable documentation for the annotation: what it does and
	// who sets it.
	Help string
	// Values, when non-empty, enumerates the well known values the annotation can take.
	Values []docs.Value
	// Stage is the stability of the annotation.
	Stage docs.Stage
	// InternalOnly annotations are set by the operator itself and must never be
	// set by users or other controllers. They are not a user-facing API, so they
	// are always docs.Alpha.
	InternalOnly bool
}

// Label is the source-of-truth description of a well known label. Declaring
// labels as Labels lets a documentation generator emit a reference page of every
// label an operator reads or writes, in the style of
// https://kubernetes.io/docs/reference/labels-annotations-taints/.
//
// Conventions:
//   - Describe every label an operator reads or writes with a Label.
//   - Values, when set, MUST be sourced from consts, never magic strings.
type Label struct {
	// Name is the label key, e.g. "karpenter.sh/nodepool".
	Name string
	// Example is a representative value, rendered as `<Name>: "<Example>"`.
	Example string
	// UsedOn lists the kinds of object the label is read from or written to,
	// e.g. &corev1.Node{}. Resolve an entry's kind with object.GVK.
	UsedOn []runtime.Object
	// Help is human-readable documentation for the label: what it does and who
	// sets it.
	Help string
	// Values, when non-empty, enumerates the well known values the label can take.
	Values []docs.Value
	// Stage is the stability of the label.
	Stage docs.Stage
	// InternalOnly labels are set by the operator itself and must never be set or
	// selected on by users or other controllers. They are not a user-facing API, so
	// they are always docs.Alpha. A label the operator sets but users select on,
	// e.g. in a nodeSelector, is not InternalOnly.
	InternalOnly bool
}

// Taint is the source-of-truth description of a well known taint. Declaring
// taints as Taints lets a documentation generator emit a reference page of every
// taint an operator adds or removes, in the style of
// https://kubernetes.io/docs/reference/labels-annotations-taints/.
//
// Conventions:
//   - Describe every taint an operator adds or removes with a Taint.
type Taint struct {
	// Taint is the taint's key, value, and effect, e.g.
	// corev1.Taint{Key: "karpenter.sh/disrupted", Effect: corev1.TaintEffectNoSchedule}.
	// It is rendered as `<Key>: "<Effect>"`.
	Taint corev1.Taint
	// UsedOn lists the kinds of object the taint is added to, e.g. &corev1.Node{}.
	// Resolve an entry's kind with object.GVK.
	UsedOn []runtime.Object
	// Help is human-readable documentation for the taint: when it is added, when
	// it is removed, and what tolerating it does.
	Help string
	// Stage is the stability of the taint.
	Stage docs.Stage
	// InternalOnly taints are added and removed by the operator itself and must
	// never be added, removed, or tolerated by users or other controllers. They
	// are not a user-facing API, so they are always docs.Alpha.
	InternalOnly bool
}
