package outside

// This file lives outside test/e2e/v2/ so the guestcluster analyzer should
// skip it entirely. It has no expected-diagnostic annotations, so any
// diagnostic reported here would be a test failure, proving the
// path-exclusion logic works.

func SomeFunction() {
	message := "checking guest cluster status"
	_ = message
	another := `guest cluster in raw literal`
	_ = another
}
