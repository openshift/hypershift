package aws

import (
	"errors"
	"testing"

	"github.com/openshift/hypershift/cmd/util"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNewCommand(t *testing.T) {
	wantErr := errors.New("injected client error")
	cmd := NewCommand(&util.ClientProvider{
		ControllerRuntimeClient: func(string) (crclient.Client, error) {
			return nil, wantErr
		},
	})
	cmd.SetArgs([]string{"--name=cluster", "--output-dir=" + t.TempDir()})

	if err := cmd.Execute(); !errors.Is(err, wantErr) {
		t.Fatalf("expected injected client error, got %v", err)
	}
}

func TestConsoleLogOptsRun(t *testing.T) {
	err := (&ConsoleLogOpts{Name: "cluster"}).Run(t.Context(), nil)
	if err == nil {
		t.Fatal("expected an error when the management client is nil")
	}
}
