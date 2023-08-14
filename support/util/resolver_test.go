package util

import "testing"

func TestFirstNameServer(t *testing.T) {
	resolvConf := `
# This is a test resolv.conf file

search example.com company.net
nameserver    192.168.0.100
nameserver    8.8.8.8	
`
	ns, err := firstNameServer([]byte(resolvConf))
	if err != nil {
		t.Fatalf("Unexpexted error: %v", err)
	}
	if ns != "192.168.0.100" {
		t.Errorf("did not get expected nameserver")
	}
}
