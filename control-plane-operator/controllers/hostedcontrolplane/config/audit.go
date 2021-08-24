package config

import (
	"bytes"

	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1beta1"

	"github.com/openshift/hypershift/control-plane-operator/api"
)

func SerializeAuditPolicy(policy *auditv1.Policy) ([]byte, error) {
	out := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(policy, out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
