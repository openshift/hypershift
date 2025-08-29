package sharedingressconfiggenerator

import (
	"fmt"
	"io"
	"net"
	"time"
)

// haProxyClient defines the interface for communicating with HAProxy.
type haProxyClient interface {
	// sendHAProxyCommand connects to the specified Unix socket, sends a command,
	// and returns the response from HAProxy.
	sendCommand(socketPath, command string) (string, error)
}

type defaultHAproxyClient struct {
}

func (c *defaultHAproxyClient) sendCommand(socketPath, command string) (string, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return "", fmt.Errorf("failed to connect to socket %s: %w", socketPath, err)
	}
	defer func() {
		_ = conn.Close()
	}()

	// Set a deadline for reads/writes to avoid blocking forever.
	if err := conn.SetDeadline(time.Now().Add(reloadTimeout)); err != nil {
		return "", fmt.Errorf("failed to set deadline on socket: %w", err)
	}

	// A newline character is required to terminate the command.
	_, err = conn.Write([]byte(command + "\n"))
	if err != nil {
		return "", fmt.Errorf("failed to send command to socket: %w", err)
	}

	response, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("failed to read response from socket: %w", err)
	}

	return string(response), nil
}
