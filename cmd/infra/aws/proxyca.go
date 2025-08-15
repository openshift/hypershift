package aws

import (
	"bytes"
	"fmt"
	"log"

	"golang.org/x/crypto/ssh"
)

func fetchProxyCA(publicAddr, privateAddr string, privateKey []byte) (string, error) {
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		log.Fatalf("unable to parse private key: %v", err)
	}

	config := &ssh.ClientConfig{
		User: "ec2-user",
		Auth: []ssh.AuthMethod{
			// Use the PublicKeys method for remote authentication.
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", publicAddr+":22", config)
	if err != nil {
		return "", fmt.Errorf("failed to dial: %w", err)
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			log.Printf("Failed to close SSH client: %v", closeErr)
		}
	}()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			log.Printf("Failed to close SSH session: %v", closeErr)
		}
	}()

	var b bytes.Buffer
	session.Stdout = &b
	if err := session.Run(fmt.Sprintf("curl -s -x http://%s:3128 http://mitm.it/cert/pem", privateAddr)); err != nil {
		return "", fmt.Errorf("failed to run curl: %w", err)
	}
	return b.String(), nil
}
