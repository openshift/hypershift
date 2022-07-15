package provisioning

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

const boundSAKeyFilename = "bound-service-account-signing-key.key"

type options struct {
	TargetDir string
}

var (
	// CreateKeyPairOpts captures the options that affect creation
	// of the key pair.
	CreateKeyPairOpts = options{
		TargetDir: "",
	}
)

func CreateKeys(prefixDir string) error {

	privateKeyFilePath := filepath.Join(prefixDir, PrivateKeyFile)
	publicKeyFilePath := filepath.Join(prefixDir, PublicKeyFile)
	bitSize := 4096

	defer copyPrivateKeyForInstaller(privateKeyFilePath, prefixDir)

	_, err := os.Stat(privateKeyFilePath)
	if err == nil {
		log.Printf("Using existing RSA keypair found at %s", privateKeyFilePath)
		return nil
	}

	log.Print("Generating RSA keypair")
	privateKey, err := rsa.GenerateKey(rand.Reader, bitSize)
	if err != nil {
		return errors.Wrap(err, "failed to generate private key")
	}

	log.Print("Writing private key to ", privateKeyFilePath)
	f, err := os.Create(privateKeyFilePath)
	if err != nil {
		return errors.Wrap(err, "failed to create private key file")
	}

	err = pem.Encode(f, &pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: nil,
		Bytes:   x509.MarshalPKCS1PrivateKey(privateKey),
	})
	f.Close()
	if err != nil {
		return errors.Wrap(err, "failed to write out private key data")
	}

	log.Print("Writing public key to ", publicKeyFilePath)
	f, err = os.Create(publicKeyFilePath)
	if err != nil {
		errors.Wrap(err, "failed to create public key file")
	}

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return errors.Wrap(err, "failed to generate public key from private")
	}

	err = pem.Encode(f, &pem.Block{
		Type:    "PUBLIC KEY",
		Headers: nil,
		Bytes:   pubKeyBytes,
	})
	f.Close()
	if err != nil {
		return errors.Wrap(err, "failed to write out public key data")
	}

	return nil
}

func copyPrivateKeyForInstaller(sourceFile, prefixDir string) {
	privateKeyForInstaller := filepath.Join(prefixDir, TLSDirName, boundSAKeyFilename)

	log.Print("Copying signing key for use by installer")
	from, err := os.Open(sourceFile)
	if err != nil {
		log.Fatalf("failed to open privatekeyfile for copying: %s", err)
	}
	defer from.Close()

	to, err := os.OpenFile(privateKeyForInstaller, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		log.Fatalf("failed to open/create target bound serviceaccount file: %s", err)
	}
	defer to.Close()

	_, err = io.Copy(to, from)
	if err != nil {
		log.Fatalf("failed to copy file: %s", err)
	}
}

func CreateKeyPairCmd(cmd *cobra.Command, args []string) {
	err := CreateKeys(CreateKeyPairOpts.TargetDir)
	if err != nil {
		log.Fatal(err)
	}
}

// initEnvForCreateKeyPairCmd will ensure the destination directory is ready to receive the generated
// files, and will create the directory if necessary.
func initEnvForCreateKeyPairCmd(cmd *cobra.Command, args []string) {
	if CreateKeyPairOpts.TargetDir == "" {
		pwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("Failed to get current directory: %s", err)
		}

		CreateKeyPairOpts.TargetDir = pwd
	}

	fPath, err := filepath.Abs(CreateKeyPairOpts.TargetDir)
	if err != nil {
		log.Fatalf("Failed to resolve full path: %s", err)
	}

	// create target dir if necessary
	err = EnsureDir(fPath)
	if err != nil {
		log.Fatalf("failed to create target directory at %s", fPath)
	}

	// create tls dir if necessary
	tlsDir := filepath.Join(fPath, TLSDirName)
	err = EnsureDir(tlsDir)
	if err != nil {
		log.Fatalf("failed to create tls directory at %s", tlsDir)
	}
}

// NewCreateKeyPairCmd provides the "create-key-pair" subcommand
func NewCreateKeyPairCmd() *cobra.Command {
	CreateKeyPairCmd := &cobra.Command{
		Use:              "create-key-pair",
		Short:            "Create a key pair",
		Run:              CreateKeyPairCmd,
		PersistentPreRun: initEnvForCreateKeyPairCmd,
	}

	CreateKeyPairCmd.PersistentFlags().StringVar(&CreateKeyPairOpts.TargetDir, "output-dir", "", "Directory to place generated files (defaults to current directory)")

	return CreateKeyPairCmd
}
