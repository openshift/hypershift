package bad

// String literal with banned terminology
func TestBadStringLiteral() {
	message := "checking guest cluster status" // want `use .* instead of .*`
	_ = message
}

// String with camelCase variant
func TestBadCamelCase() {
	message := "the guestCluster is ready" // want `use .* instead of .*`
	_ = message
}

// String with PascalCase variant
func TestBadPascalCase() {
	message := "GuestCluster configuration" // want `use .* instead of .*`
	_ = message
}

// Comment contains banned terminology
// This function checks the guest cluster readiness // want `use .* instead of .*`
func TestCommentViolation() {
	// The guest cluster should be ready // want `use .* instead of .*`
	_ = "ok"
}

// String with lowercase no-space variant
func TestLowerCaseNoSpace() {
	message := "the guestcluster is ready" // want `use .* instead of .*`
	_ = message
}

// Multiple violations in one string
func TestMultipleViolations() {
	message := "guest cluster and guestCluster" // want `use .* instead of .*`
	_ = message
}

// String with all-caps banned term
func TestAllCaps() {
	message := "checking GUEST CLUSTER status" // want `use .* instead of .*`
	_ = message
}

// Raw string literal (backtick) with banned terminology
func TestRawStringLiteral() {
	message := `this mentions guest cluster` // want `use .* instead of .*`
	_ = message
}
