package openshiftcontrollermanager

/*
import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/yaml"

	operatorv1 "github.com/openshift/api/operator/v1"
)

const (
	configMapName  = "openshift-controller-manager-config"
	deploymentName = "openshift-controller-manager"
)

var _ v1helpers.OperatorClient = (*cmOperatorClient)(nil)

type cmOperatorClient struct {
	Client    kubeclient.Interface
	Namespace string
	Logger    logr.Logger
}

func (c *cmOperatorClient) Informer() cache.SharedIndexInformer {
	panic("informer not supported")
}

func (c *cmOperatorClient) GetObjectMeta() (meta *metav1.ObjectMeta, err error) {
	panic("getObjectMeta not supported")
}

func (c *cmOperatorClient) GetOperatorState() (spec *operatorv1.OperatorSpec, status *operatorv1.OperatorStatus, resourceVersion string, err error) {
	var cm *corev1.ConfigMap
	cm, err = c.Client.CoreV1().ConfigMaps(c.Namespace).Get(context.TODO(), configMapName, metav1.GetOptions{})
	if err != nil {
		return
	}
	configYAML := []byte(cm.Data["config.yaml"])
	var configJSON []byte
	configJSON, err = yaml.YAMLToJSON(configYAML)
	if err != nil {
		return
	}
	configJSON, err = filterManagedConfigKeys(configJSON)
	if err != nil {
		return
	}
	spec = &operatorv1.OperatorSpec{}
	status = &operatorv1.OperatorStatus{}
	spec.ObservedConfig.Raw = configJSON
	resourceVersion = cm.ResourceVersion
	return
}

// UpdateOperatorSpec updates the spec of the operator, assuming the given resource version.
func (c *cmOperatorClient) UpdateOperatorSpec(oldResourceVersion string, in *operatorv1.OperatorSpec) (out *operatorv1.OperatorSpec, newResourceVersion string, err error) {
	var cm *corev1.ConfigMap
	cm, err = c.Client.CoreV1().ConfigMaps(c.Namespace).Get(context.TODO(), configMapName, metav1.GetOptions{})
	if err != nil {
		return
	}
	if cm.ResourceVersion != oldResourceVersion {
		err = fmt.Errorf("resource version does not match")
		return
	}
	var updateJSON []byte
	updateJSON, err = in.ObservedConfig.MarshalJSON()
	if err != nil {
		return
	}
	var configBytes []byte
	configBytes, err = mergeConfig([]byte(cm.Data["config.yaml"]), updateJSON)
	if err != nil {
		return
	}
	cm.Data["config.yaml"] = string(configBytes)
	c.Logger.Info("Updating OpenShift Controller Manager configmap")
	_, err = c.Client.CoreV1().ConfigMaps(c.Namespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
	if err != nil {
		return
	}
	dataHash := calculateHash(configBytes)
	var deployment *appsv1.Deployment
	deployment, err = c.Client.AppsV1().Deployments(c.Namespace).Get(context.TODO(), deploymentName, metav1.GetOptions{})
	if err != nil {
		return
	}
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}
	c.Logger.Info("Updating OpenShift Controller Manager deployment")
	deployment.Spec.Template.ObjectMeta.Annotations["config-checksum"] = dataHash
	_, err = c.Client.AppsV1().Deployments(c.Namespace).Update(context.TODO(), deployment, metav1.UpdateOptions{})
	return
}

func mergeConfig(existingYAML, updateJSON []byte) (updatedYAML []byte, err error) {
	var existingJSON []byte
	existingJSON, err = yaml.YAMLToJSON(existingYAML)
	if err != nil {
		return
	}
	existingConfig := map[string]interface{}{}
	if err = json.NewDecoder(bytes.NewBuffer(existingJSON)).Decode(&existingConfig); err != nil {
		return
	}
	updateConfig := map[string]interface{}{}
	if err = json.NewDecoder(bytes.NewBuffer(updateJSON)).Decode(&updateConfig); err != nil {
		return
	}
	for key := range updateConfig {
		switch key {
		case "dockerPullSecret", "build", "deployer":
			existingConfig[key] = updateConfig[key]
		}
	}
	var mergedConfig []byte
	mergedConfig, err = json.Marshal(existingConfig)
	if err != nil {
		return
	}

	updatedYAML, err = yaml.JSONToYAML(mergedConfig)
	return
}

// filterManagedConfigKeys returns JSON that contains only the keys managed by the
// observer controller from a bigger config JSON
func filterManagedConfigKeys(in []byte) (out []byte, err error) {
	inputConfig := map[string]interface{}{}
	if err = json.NewDecoder(bytes.NewBuffer(in)).Decode(&inputConfig); err != nil {
		return
	}
	outputConfig := map[string]interface{}{}
	for key := range inputConfig {
		switch key {
		case "dockerPullSecret", "build", "deployer":
			outputConfig[key] = inputConfig[key]
		}
	}
	out, err = json.Marshal(outputConfig)
	return
}

// UpdateOperatorStatus updates the status of the operator, assuming the given resource version.
func (c *cmOperatorClient) UpdateOperatorStatus(oldResourceVersion string, in *operatorv1.OperatorStatus) (out *operatorv1.OperatorStatus, err error) {
	return
}

func calculateHash(b []byte) string {
	return fmt.Sprintf("%x", md5.Sum(b))
}
*/
