// This test executable forwards each invocation's arguments to the Go test and
// returns its configured stdout, stderr and exit status to the calling script.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd/testdata/fake-etcdctl/mock"
)

func main() {
	reply, err := call(os.Getenv(mock.ServerEnv), os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake etcdctl:", err)
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, reply.Stdout)
	fmt.Fprint(os.Stderr, reply.Stderr)
	os.Exit(reply.ExitCode)
}

func call(url string, args []string) (mock.Reply, error) {
	var reply mock.Reply
	body, err := json.Marshal(args)
	if err != nil {
		return reply, err
	}
	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return reply, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, err := io.ReadAll(response.Body)
		if err != nil {
			return reply, err
		}
		return reply, fmt.Errorf("mock server returned %s: %s", response.Status, message)
	}
	err = json.NewDecoder(response.Body).Decode(&reply)
	return reply, err
}
