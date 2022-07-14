package fixtures

import (
	rbacv1 "k8s.io/api/rbac/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ExampleAgentOptions struct {
	APIServerAddress string
	AgentNamespace   string
}

type ExampleAgentResources struct {
	CAPIProviderAgentRole *rbacv1.Role
}

func (o *ExampleAgentResources) AsObjects() []crclient.Object {
	return []crclient.Object{o.CAPIProviderAgentRole}
}
