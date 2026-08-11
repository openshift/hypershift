package good

// Valid: using "hosted cluster" terminology
func TestGoodUsage() {
	message := "checking hosted cluster status"
	_ = message
}

// Valid: using hostedCluster in code
func TestCamelCase() {
	hostedCluster := "my-cluster"
	_ = hostedCluster
}

// Valid: HostedCluster type name
type HostedCluster struct {
	Name string
}

// Valid: Go identifier containing the banned term should NOT be flagged.
// The analyzer only checks string literals and comments, not identifier names.
func TestIdentifierNotFlagged() {
	var guestCluster string = "some value"
	_ = guestCluster
	guestClusterName := "another value"
	_ = guestClusterName
}

// Valid: raw string literal with correct terminology
func TestRawStringLiteral() {
	message := `this mentions hosted cluster`
	_ = message
}
