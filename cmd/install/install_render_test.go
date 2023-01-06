package install

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/openshift/hypershift/cmd/install/assets"
	"gopkg.in/yaml.v2"
)

func ExecuteTestCommand(args []string) ([]byte, error) {
	cmd := NewCommand()
	cmd.SetArgs(args)
	b := bytes.NewBufferString("")
	cmd.SetOut(b)
	err := cmd.Execute()
	if err != nil {
		return []byte{}, err
	}
	return io.ReadAll(b)
}

func ExecuteTemplateYamlGenerationCommand(args []string) (map[string]interface{}, error) {
	out, err := ExecuteTestCommand(args)
	if err != nil {
		return nil, err
	}

	var template map[string]interface{}
	if err := yaml.Unmarshal(out, &template); err != nil {
		return nil, err
	}

	return template, nil
}

func VerifyTemplateParameterPresent(template map[string]interface{}, paramName string) bool {
	params := template["parameters"].([]interface{})
	for _, p := range params {
		if name, namePresent := p.(map[interface{}]interface{})["name"]; namePresent {
			if name == paramName {
				return true
			}
		}
	}
	return false
}

func TestMultiDocYamlRendering(t *testing.T) {
	out, err := ExecuteTestCommand([]string{"--oidc-storage-provider-s3-bucket-name", "bucket", "--oidc-storage-provider-s3-secret", "secret", "--oidc-storage-provider-s3-region", "us-east-1", "--oidc-storage-provider-s3-bucket-name", "mybucket", "render", "--format", "yaml"})
	if err != nil {
		t.Fatal(err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(out))
	var manifest map[string]interface{}
	cnt := 0
	for dec.Decode(&manifest) == nil {
		cnt += 1
	}
	if cnt < 2 {
		t.Fatal("no manifests found")
	}
}

func TestTemplateYamlRendering(t *testing.T) {
	template, err := ExecuteTemplateYamlGenerationCommand([]string{"--oidc-storage-provider-s3-bucket-name", "bucket", "--oidc-storage-provider-s3-region", "us-east-1", "--oidc-storage-provider-s3-secret", "secret", "render", "--format", "yaml", "--template"})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := template["objects"]; !ok {
		t.Fatal("objects missing in template")
	}
	objects := template["objects"].([]interface{})
	if len(objects) == 0 {
		t.Fatal("no objects found in template")
	}
	params := []string{
		"OPERATOR_REPLICAS", "OPERATOR_IMG", "IMAGE_TAG", "NAMESPACE",
		"OIDC_S3_NAME", "OIDC_S3_REGION", "OIDC_S3_CREDS_SECRET",
		"OIDC_S3_CREDS_SECRET_KEY",
	}
	for _, param := range params {
		if !VerifyTemplateParameterPresent(template, param) {
			t.Fatal("expected parameter", param, "not found")
		}
	}
}

func selectorSyncSetFromTemplateYaml(template map[string]interface{}, t *testing.T) map[interface{}]interface{} {
	if _, ok := template["objects"]; !ok {
		t.Fatal("objects missing in template")
	}
	objects := template["objects"].([]interface{})
	if len(objects) != 1 {
		t.Fatal("no single object found in template")
	}
	sss := objects[0].(map[interface{}]interface{})
	if sss["kind"] != "SelectorSyncSet" {
		t.Fatal("no SelectorSyncSet object found in template")
	}
	return sss
}

func deploymentEnvVarsFromSelectorSyncSet(sss map[interface{}]interface{}, name string, containerId int) []interface{} {
	spec := sss["spec"].(map[interface{}]interface{})
	resources := spec["resources"].([]interface{})
	for _, res := range resources {
		r := res.(map[interface{}]interface{})
		if r["kind"].(string) != "Deployment" {
			continue
		}
		if r["metadata"].(map[interface{}]interface{})["name"].(string) != name {
			continue
		}
		deploymentSpec := r["spec"].(map[interface{}]interface{})
		deploymentTemplate := deploymentSpec["template"].(map[interface{}]interface{})
		deploymentTemplateSpec := deploymentTemplate["spec"].(map[interface{}]interface{})
		container := deploymentTemplateSpec["containers"].([]interface{})[containerId]
		envVars := container.(map[interface{}]interface{})["env"]
		return envVars.([]interface{})
	}
	return nil
}

func envVarWithName(envVars []interface{}, name string) map[interface{}]interface{} {
	for _, env := range envVars {
		envVar := env.(map[interface{}]interface{})
		if envVar["name"].(string) == name {
			return envVar
		}
	}
	return nil
}

type isEnvVarValid func(map[interface{}]interface{}) bool

func isEnvVarValueFromSecretKeyRef(envVar map[interface{}]interface{}) bool {
	valueFrom, ok := envVar["valueFrom"]
	if !ok {
		return false
	}
	_, ok = valueFrom.(map[interface{}]interface{})["secretKeyRef"]
	return ok
}

func isEnvVarSimpleValue(envVar map[interface{}]interface{}) bool {
	_, ok := envVar["value"]
	return ok
}

func validateEnvVars(sss map[interface{}]interface{}, name string, containerId int, envVarNames []string, validate isEnvVarValid, t *testing.T) {
	deploymentContainerEnvVars := deploymentEnvVarsFromSelectorSyncSet(sss, name, 0)
	if deploymentContainerEnvVars == nil {
		t.Fatal("external-dns deployment not found")
	}
	if len(deploymentContainerEnvVars) == 0 {
		t.Fatal("no environment variable found in external-dns deployment")
	}
	for _, envVarName := range envVarNames {
		envVar := envVarWithName(deploymentContainerEnvVars, envVarName)
		if envVar == nil {
			t.Fatal("no environment variable named", envVarName, "found in external-dns deployment")
		}
		if !validate(envVar) {
			t.Fatal(name, " deployment env var ", envVarName, " does not refer to a secretKeyRef")
		}
	}
}

func TestSSSTemplateYamlRenderingWithSecretKeyRefs(t *testing.T) {
	template, err := ExecuteTemplateYamlGenerationCommand([]string{
		"render", "--format", "yaml", "--sss-template",
		"--private-platform=AWS",
		"--aws-private-region-secret=aws-credentials", "--aws-private-region-secret-key=region",
		"--aws-private-secret=aws-credentials", "--aws-private-secret-key=credentials",
		"--external-dns-provider=aws",
		"--external-dns-secret=dns-credentials",
		"--external-dns-domain-filter-secret=dns-credentials", "--external-dns-domain-filter-secret-key=domain-filter",
		"--external-dns-txt-owner-id-secret=dns-credentials", "--external-dns-txt-owner-id-secret-key=txt-owner-id",
	})

	if err != nil {
		t.Fatal(err)
	}

	params := []string{
		"OPERATOR_REPLICAS", "OPERATOR_IMG", "IMAGE_TAG", "NAMESPACE",
		"AWS_PRIVATE_CREDS_SECRET", "AWS_PRIVATE_CREDS_SECRET_KEY",
		"AWS_PRIVATE_REGION_SECRET", "AWS_PRIVATE_REGION_SECRET_KEY",
		"EXTERNAL_DNS_CREDS_SECRET",
		"EXTERNAL_DNS_DOMAIN_FILTER_SECRET", "EXTERNAL_DNS_DOMAIN_FILTER_SECRET_KEY",
		"EXTERNAL_DNS_TXT_OWNER_ID_SECRET", "EXTERNAL_DNS_TXT_OWNER_ID_SECRET_KEY",
	}
	for _, param := range params {
		if !VerifyTemplateParameterPresent(template, param) {
			t.Fatal("expected parameter", param, "not found")
		}
	}

	sss := selectorSyncSetFromTemplateYaml(template, t)

	deploymentName := "external-dns"
	envVarNames := []string{assets.ExternalDNSEnvVarDomainFilter, assets.ExternalDNSEnvVarTxtOwnerID}
	validateEnvVars(sss, deploymentName, 0, envVarNames, isEnvVarValueFromSecretKeyRef, t)

	deploymentName = "operator"
	envVarNames = []string{"AWS_REGION"}
	validateEnvVars(sss, deploymentName, 0, envVarNames, isEnvVarValueFromSecretKeyRef, t)
}

func TestSSSTemplateYamlRenderingWithValues(t *testing.T) {
	template, err := ExecuteTemplateYamlGenerationCommand([]string{
		"render", "--format", "yaml", "--sss-template",
		"--private-platform=AWS",
		"--aws-private-region=us-east-1",
		"--aws-private-secret=aws-credentials",
		"--aws-private-secret-key=credentials",
		"--external-dns-provider=aws",
		"--external-dns-secret=dns-credentials",
		"--external-dns-domain-filter=mydomain.hypershift.net",
		"--external-dns-txt-owner-id=ThisIsMyOwnerId",
	})

	if err != nil {
		t.Fatal(err)
	}

	sss := selectorSyncSetFromTemplateYaml(template, t)

	params := []string{
		"OPERATOR_REPLICAS", "OPERATOR_IMG", "IMAGE_TAG", "NAMESPACE",
		"AWS_PRIVATE_CREDS_SECRET", "AWS_PRIVATE_CREDS_SECRET_KEY",
		"AWS_PRIVATE_REGION",
		"EXTERNAL_DNS_CREDS_SECRET",
		"EXTERNAL_DNS_DOMAIN_FILTER",
		"EXTERNAL_DNS_TXT_OWNER_ID",
	}
	for _, param := range params {
		if !VerifyTemplateParameterPresent(template, param) {
			t.Fatal("expected parameter", param, "not found")
		}
	}

	deploymentName := "external-dns"
	envVarNames := []string{assets.ExternalDNSEnvVarDomainFilter, assets.ExternalDNSEnvVarTxtOwnerID}
	validateEnvVars(sss, deploymentName, 0, envVarNames, isEnvVarSimpleValue, t)

	deploymentName = "operator"
	envVarNames = []string{"AWS_REGION"}
	validateEnvVars(sss, deploymentName, 0, envVarNames, isEnvVarSimpleValue, t)
}

func ExecuteJsonGenerationCommand(args []string) (map[string]interface{}, error) {
	out, err := ExecuteTestCommand(args)
	if err != nil {
		return nil, err
	}

	var doc map[string]interface{}
	json.Unmarshal(out, &doc)

	return doc, nil
}

func TestJsonListRendering(t *testing.T) {
	doc, err := ExecuteJsonGenerationCommand([]string{"--oidc-storage-provider-s3-bucket-name", "bucket", "--oidc-storage-provider-s3-region", "us-east-1", "--oidc-storage-provider-s3-secret", "secret", "render", "--format", "json"})
	if err != nil {
		t.Fatal(err)
	}

	if doc["kind"] != "List" {
		t.Fatal("expected kind List")
	}
	items := doc["items"].([]interface{})
	if len(items) == 0 {
		t.Fatal("no objects in items of json List")
	}
}

func TestJsonTemplateRendering(t *testing.T) {
	doc, err := ExecuteJsonGenerationCommand([]string{"--oidc-storage-provider-s3-bucket-name", "bucket", "--oidc-storage-provider-s3-region", "us-east-1", "--oidc-storage-provider-s3-secret", "secret", "render", "--format", "json", "--template"})
	if err != nil {
		t.Fatal(err)
	}

	if doc["kind"] != "Template" {
		t.Fatal("expected kind Template")
	}
	objects := doc["objects"].([]interface{})
	if len(objects) == 0 {
		t.Fatal("no objects in template objects")
	}
}

func TestJsonSSSTemplateRendering(t *testing.T) {
	doc, err := ExecuteJsonGenerationCommand([]string{"--oidc-storage-provider-s3-bucket-name", "bucket", "--oidc-storage-provider-s3-region", "us-east-1", "--oidc-storage-provider-s3-secret", "secret", "render", "--format", "json", "--sss-template"})
	if err != nil {
		t.Fatal(err)
	}

	if doc["kind"] != "Template" {
		t.Fatal("expected kind Template")
	}
	objects := doc["objects"].([]interface{})
	if len(objects) != 1 {
		t.Fatal("no single object in template objects")
	}
	sss := objects[0].(map[string]interface{})
	if sss["kind"] != "SelectorSyncSet" {
		t.Fatal("element in template objects in not a SelectorSyncSet")
	}
}
