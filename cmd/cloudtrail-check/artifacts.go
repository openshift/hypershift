package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	kindHostedCluster  = "HostedCluster"
	kindPod            = "Pod"
	kindServiceAccount = "ServiceAccount"
	irsaRoleAnnotation = "eks.amazonaws.com/role-arn"
)

type artifactInventory struct {
	RoleARNs []string `json:"roleARNs"`
	Regions  []string `json:"regions"`
}

// scanArtifacts reads Kubernetes YAML/JSON objects recursively from a dump directory.
func scanArtifacts(root string) (artifactInventory, error) {
	roles := map[string]struct{}{}
	regions := map[string]struct{}{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			return nil
		}
		if err := scanArtifactFile(path, roles, regions); err != nil {
			return fmt.Errorf("scan %s: %w", path, err)
		}
		return nil
	}); err != nil {
		return artifactInventory{}, err
	}

	inventory := artifactInventory{
		RoleARNs: sortedKeys(roles),
		Regions:  sortedKeys(regions),
	}
	return inventory, nil
}

func scanArtifactFile(path string, roles, regions map[string]struct{}) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := utilyaml.NewYAMLOrJSONDecoder(file, 4096)
	for {
		var object map[string]interface{}
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(object) == 0 {
			continue
		}
		collectObject(object, roles, regions)
	}
}

func collectObject(object map[string]interface{}, roles, regions map[string]struct{}) {
	kind, _ := object["kind"].(string)
	switch kind {
	case kindHostedCluster:
		spec := mapValue(object, "spec")
		platform := mapValue(spec, "platform")
		awsPlatform := mapValue(platform, "aws")
		addValue(regions, stringValue(awsPlatform["region"]))
		collectRoleARNs(mapValue(awsPlatform, "rolesRef"), roles)
		secretEncryption := mapValue(spec, "secretEncryption")
		kms := mapValue(secretEncryption, "kms")
		awsKMS := mapValue(kms, "aws")
		auth := mapValue(awsKMS, "auth")
		addRole(roles, stringValue(auth["awsKMSRoleARN"]))
	case kindPod:
		spec := mapValue(object, "spec")
		for _, field := range []string{"containers", "initContainers", "ephemeralContainers"} {
			containers, _ := spec[field].([]interface{})
			for _, rawContainer := range containers {
				container, _ := rawContainer.(map[string]interface{})
				envs, _ := container["env"].([]interface{})
				for _, rawEnv := range envs {
					env, _ := rawEnv.(map[string]interface{})
					if stringValue(env["name"]) == "AWS_ROLE_ARN" {
						addRole(roles, stringValue(env["value"]))
					}
				}
			}
		}
	case kindServiceAccount:
		metadata := mapValue(object, "metadata")
		annotations := mapValue(metadata, "annotations")
		addRole(roles, stringValue(annotations[irsaRoleAnnotation]))
	}
}

func collectRoleARNs(values map[string]interface{}, roles map[string]struct{}) {
	for _, value := range values {
		addRole(roles, stringValue(value))
	}
}

func addRole(roles map[string]struct{}, arn string) {
	if strings.HasPrefix(arn, "arn:") && strings.Contains(arn, ":role/") {
		roles[arn] = struct{}{}
	}
}

func addValue(values map[string]struct{}, value string) {
	if value != "" {
		values[value] = struct{}{}
	}
}

func mapValue(values map[string]interface{}, key string) map[string]interface{} {
	value, _ := values[key].(map[string]interface{})
	return value
}

func stringValue(value interface{}) string {
	result, _ := value.(string)
	return result
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
