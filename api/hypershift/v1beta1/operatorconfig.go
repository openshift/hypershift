package v1beta1

func (c *OperatorConfiguration) GetAuthentication() AuthenticationOperatorSpec {
	return c.Authentication
}
