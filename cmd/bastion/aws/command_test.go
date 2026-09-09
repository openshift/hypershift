package aws

import (
	"errors"
	"testing"

	"github.com/openshift/hypershift/cmd/util"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

func TestNewCreateCommand(t *testing.T) {
	wantErr := errors.New("injected client error")
	cmd := NewCreateCommand(&util.ClientProvider{
		ControllerRuntimeClient: func(string) (crclient.Client, error) {
			return nil, wantErr
		},
	})
	cmd.SetArgs([]string{"--name=cluster", "--aws-creds=/tmp/credentials"})

	if err := cmd.Execute(); !errors.Is(err, wantErr) {
		t.Fatalf("expected injected client error, got %v", err)
	}
}

func TestNewDestroyCommand(t *testing.T) {
	wantErr := errors.New("injected client error")
	cmd := NewDestroyCommand(&util.ClientProvider{
		ControllerRuntimeClient: func(string) (crclient.Client, error) {
			return nil, wantErr
		},
	})
	cmd.SetArgs([]string{"--name=cluster", "--aws-creds=/tmp/credentials"})

	if err := cmd.Execute(); !errors.Is(err, wantErr) {
		t.Fatalf("expected injected client error, got %v", err)
	}
}

func TestCreateBastionOptsRun(t *testing.T) {
	_, _, err := (&CreateBastionOpts{Name: "cluster"}).Run(t.Context(), logr.Discard(), nil)
	if err == nil {
		t.Fatal("expected an error when the management client is nil")
	}
}

func TestDestroyBastionOptsRun(t *testing.T) {
	err := (&DestroyBastionOpts{Name: "cluster"}).Run(t.Context(), logr.Discard(), nil)
	if err == nil {
		t.Fatal("expected an error when the management client is nil")
	}
}
