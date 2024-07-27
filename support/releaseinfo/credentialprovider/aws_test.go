package credentialprovider

import (
	"context"
	"reflect"
	"testing"
)

func TestGetECRCredentials(t *testing.T) {
	type args struct {
		ctx     context.Context
		ecrRepo *ECRRepo
	}
	tests := []struct {
		name    string
		e       *ecrDockerCredentialProviderImpl
		args    args
		want    string
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &ecrDockerCredentialProviderImpl{}
			got, err := e.GetECRCredentials(tt.args.ctx, tt.args.ecrRepo)
			if (err != nil) != tt.wantErr {
				t.Errorf("ecrDockerCredentialProviderImpl.GetECRCredentials() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ecrDockerCredentialProviderImpl.GetECRCredentials() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseECRRepoURL(t *testing.T) {
	type args struct {
		image string
	}
	tests := []struct {
		name    string
		e       *ecrDockerCredentialProviderImpl
		args    args
		want    *ECRRepo
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &ecrDockerCredentialProviderImpl{}
			got, err := e.ParseECRRepoURL(tt.args.image)
			if (err != nil) != tt.wantErr {
				t.Errorf("ecrDockerCredentialProviderImpl.ParseECRRepoURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ecrDockerCredentialProviderImpl.ParseECRRepoURL() = %v, want %v", got, tt.want)
			}
		})
	}
}
