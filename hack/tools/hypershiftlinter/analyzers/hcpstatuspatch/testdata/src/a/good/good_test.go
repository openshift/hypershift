package good

// Seeding fixture status via Status().Update() in a unit test is fine — there's
// no concurrent writer to race with in a synchronous test.
func seedFixtureStatus(c Client, hcp *HostedControlPlane) error {
	return c.Status().Update(nil, hcp)
}
