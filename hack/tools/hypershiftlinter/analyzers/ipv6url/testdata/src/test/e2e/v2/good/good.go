package good

import (
	"fmt"
	"log"
	"net"
)

// Valid: using net.JoinHostPort for URL construction
func TestGoodUsage() {
	host := "192.168.1.1"
	port := 8080
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	url := fmt.Sprintf("http://%s/api", addr)
	_ = url
}

// Valid: fmt.Sprintf without host:port pattern
func TestOtherFormats() {
	name := "test"
	value := 42
	message := fmt.Sprintf("name=%s value=%d", name, value)
	_ = message
}

// Valid: single %s without port
func TestNoPort() {
	host := "example.com"
	url := fmt.Sprintf("http://%s/path", host)
	_ = url
}

// Valid: log.Printf with %s:%d pattern (analyzer only checks fmt format funcs)
func TestLogPrintfNotFlagged() {
	host := "192.168.1.1"
	port := 8080
	log.Printf("http://%s:%d", host, port)
}

// Valid: fmt.Errorf without host:port pattern
func TestErrorfNoHostPort() {
	err := fmt.Errorf("failed to connect: %s", "timeout")
	_ = err
}
