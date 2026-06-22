package config

import (
	"bytes"

	"github.com/openshift/hypershift/support/api"

	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1"
)

func SerializeAuditPolicy(policy *auditv1.Policy) ([]byte, error) {
	out := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(policy, out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
