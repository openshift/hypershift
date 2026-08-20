package bad

import "fmt"

// Invalid: fmt.Sprintf with %s:%d pattern
func TestBadPortD() {
	host := "192.168.1.1"
	port := 8080
	url := fmt.Sprintf("http://%s:%d/api", host, port) // want `fmt\.Sprintf with %s:%d may produce invalid URLs for IPv6 addresses; use net\.JoinHostPort instead`
	_ = url
}

// Invalid: fmt.Sprintf with %s:%v pattern
func TestBadPortV() {
	host := "192.168.1.1"
	port := 8080
	url := fmt.Sprintf("http://%s:%v/api", host, port) // want `fmt\.Sprintf with %s:%v may produce invalid URLs for IPv6 addresses; use net\.JoinHostPort instead`
	_ = url
}

// Invalid: simple %s:%d without http prefix
func TestSimpleHostPort() {
	host := "localhost"
	port := 9090
	addr := fmt.Sprintf("%s:%d", host, port) // want `fmt\.Sprintf with %s:%d may produce invalid URLs for IPv6 addresses; use net\.JoinHostPort instead`
	_ = addr
}

// Invalid: fmt.Errorf with %s:%d pattern
func TestBadErrorf() {
	host := "192.168.1.1"
	port := 8080
	err := fmt.Errorf("connect to %s:%d", host, port) // want `fmt\.Errorf with %s:%d may produce invalid URLs for IPv6 addresses; use net\.JoinHostPort instead`
	_ = err
}

// Invalid: fmt.Sprintf with %s:%s pattern
func TestBadPortS() {
	host := "192.168.1.1"
	portStr := "8080"
	url := fmt.Sprintf("http://%s:%s", host, portStr) // want `fmt\.Sprintf with %s:%s may produce invalid URLs for IPv6 addresses; use net\.JoinHostPort instead`
	_ = url
}
