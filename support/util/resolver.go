package util

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
)

func NameServerIP() (string, error) {
	// Parse /etc/resolv.conf
	b, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return "", err
	}
	return firstNameServer(b)
}

func firstNameServer(content []byte) (string, error) {
	in := bytes.NewBuffer(content)
	scanner := bufio.NewScanner(in)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 0 && (line[0] == ';' || line[0] == '#') {
			// comment.
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		if parts[0] == "nameserver" {
			return parts[1], nil
		}
	}
	return "", fmt.Errorf("no nameserver found")
}
