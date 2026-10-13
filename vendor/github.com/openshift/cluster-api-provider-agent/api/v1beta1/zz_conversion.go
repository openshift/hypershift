package v1beta1

import (
	"bytes"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

var (
	localScheme = runtime.NewScheme()
	serializer  = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, localScheme, localScheme,
		json.SerializerOptions{Strict: false},
	)
)

func init() {
	v1alpha1.AddToScheme(localScheme)
	configv1.AddToScheme(localScheme)
	clientgoscheme.AddToScheme(localScheme)
	AddToScheme(localScheme)
}

func (e *AgentCluster) ConvertTo(rawDst conversion.Hub) error {
	return serializationConvert(e, rawDst)
}
func (e *AgentCluster) ConvertFrom(rawSrc conversion.Hub) error {
	return serializationConvert(rawSrc, e)
}
func (e *AgentMachine) ConvertTo(rawDst conversion.Hub) error {
	return serializationConvert(e, rawDst)
}
func (e *AgentMachine) ConvertFrom(rawSrc conversion.Hub) error {
	return serializationConvert(rawSrc, e)
}
func (e *AgentMachineTemplate) ConvertTo(rawDst conversion.Hub) error {
	return serializationConvert(e, rawDst)
}
func (e *AgentMachineTemplate) ConvertFrom(rawSrc conversion.Hub) error {
	return serializationConvert(rawSrc, e)
}
func serializationConvert(from runtime.Object, to runtime.Object) error {
	b := &bytes.Buffer{}
	from.GetObjectKind().SetGroupVersionKind(schema.GroupVersionKind{})
	if err := serializer.Encode(from, b); err != nil {
		return fmt.Errorf("cannot serialize %T: %w", from, err)
	}
	if _, _, err := serializer.Decode(b.Bytes(), nil, to); err != nil {
		return fmt.Errorf("cannot decode %T: %w", to, err)
	}
	gvks, _, err := localScheme.ObjectKinds(to)
	if err != nil || len(gvks) == 0 {
		return fmt.Errorf("cannot get gvk for %T: %w", to, err)
	}
	to.GetObjectKind().SetGroupVersionKind(gvks[0])
	return nil
}
