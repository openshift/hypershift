package members

import d "github.com/openshift/hypershift/test/e2e/util/dump"

// Parameter types are checked in the same scope as the body.
func Existing(other *d.Other, embedded *d.Embedded, alias *d.Alias) { // want `reference to .*dump.Other is not allowed` `reference to .*dump.Embedded is not allowed` `reference to .*dump.Alias is not allowed`
	good := d.Diagnostics{Message: "existing"}
	_ = good.Message
	good.Collect()
	other.Collect()             // want `reference to .*dump.Other.Collect is not allowed`
	_ = other.Message           // want `reference to .*dump.Other.Message is not allowed`
	_ = d.Other{Message: "new"} // want `reference to .*dump.Other is not allowed` `reference to .*dump.Other.Message is not allowed`
	embedded.Collect()
	_ = embedded.Message
	alias.Collect()
	_ = alias.Message
	_ = d.Diagnostics{Message: "elided"}
	_ = []d.Diagnostics{{Message: "nested"}}
	_ = (*d.Diagnostics).Collect
}
