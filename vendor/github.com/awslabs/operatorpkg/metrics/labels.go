package metrics

// Label is the source-of-truth description of a Prometheus metric label (a
// "dimension"). Declaring dimensions as Labels lets a metrics documentation
// generator emit per-dimension help text and, where the set of possible values
// is known and stable, the list of values.
//
// operatorpkg exports the type (and shared operator dimensions) so that both
// operatorpkg's own metrics and downstream projects (e.g. Karpenter) can
// describe their dimensions with a single, consistent type.
//
// Conventions:
//   - Describe a metric dimension with a Label; avoid a bare string literal in
//     the metric's label-names slice where a documented Label already exists.
//   - Values, when set, MUST be a list of consts, never magic strings.
//   - Before adding a new Label, check whether an existing one already describes
//     the dimension you need and reference it instead.
type Label struct {
	// Name is the Prometheus label name (the dimension key), e.g. "kind".
	Name string
	// Help is human-readable documentation for the dimension.
	Help string
	// Values, when non-empty, enumerates the stable set of values the dimension
	// can take, each with its own documentation. Every value's Name MUST be
	// sourced from a const, never a magic string.
	Values []Value
}

// Value documents one of the stable values a metric dimension (Label) can take.
type Value struct {
	// Name is the dimension value. It MUST be sourced from a const, never a magic
	// string.
	Name string
	// Help is human-readable documentation for this value: what it means and,
	// where useful, why the dimension takes it.
	Help string
}

// Shared operator metric dimensions. The label-name consts (LabelGroup, etc.)
// remain the values used in metric label-names slices so existing declarations
// compile unchanged; these Label vars carry the documentation keyed by the same
// name.
var (
	Group = Label{
		Name: LabelGroup,
		Help: "The API group of the object the metric describes, e.g. `karpenter.sh`.",
	}
	Kind = Label{
		Name: LabelKind,
		Help: "The Kind of the object the metric describes, e.g. `NodeClaim`.",
	}
	Type = Label{
		Name: LabelType,
		// `type` is reused across metric families, so document the common cases
		// rather than enumerating a fixed Values list.
		Help: "The type dimension. For status-condition metrics it is the status " +
			"condition type (e.g. `Ready`); for event metrics it is the Kubernetes " +
			"event type (`Normal` or `Warning`).",
	}
	Reason = Label{
		Name: LabelReason,
		Help: "The reason dimension. For status-condition metrics it is the condition " +
			"reason; for event metrics it is the Kubernetes event reason.",
	}
)
