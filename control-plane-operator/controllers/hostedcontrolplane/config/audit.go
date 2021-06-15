package config

import (
	"bytes"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	auditv1 "k8s.io/apiserver/pkg/apis/audit/v1beta1"
)

var (
	auditScheme     = runtime.NewScheme()
	auditSerializer = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, auditScheme, auditScheme,
		json.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
)

func init() {
	auditv1.AddToScheme(auditScheme)
}

func SerializeAuditPolicy(policy *auditv1.Policy) ([]byte, error) {
	out := &bytes.Buffer{}
	if err := auditSerializer.Encode(policy, out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
