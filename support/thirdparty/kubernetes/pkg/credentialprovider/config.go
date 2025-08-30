/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

/* Extracted from k8s.io/pkg/credentialprovider/config.go */
package credentialprovider

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DockerConfigJSON represents ~/.docker/config.json file info
// see https://github.com/docker/docker/pull/12009
type DockerConfigJSON struct {
	Auths DockerConfig `json:"auths"`
	// +optional
	HTTPHeaders map[string]string `json:"HttpHeaders,omitempty"`
}

// DockerConfig represents the config file used by the docker CLI.
// This config that represents the credentials that should be used
// when pulling images from specific image repositories.
type DockerConfig map[string]DockerConfigEntry

// DockerConfigEntry wraps a docker config as a entry
type DockerConfigEntry struct {
	Username string
	Password string
	Email    string
}

var (
	preferredPathLock sync.Mutex
	preferredPath     = ""
	workingDirPath    = ""
	homeDirPath, _    = os.UserHomeDir()
	rootDirPath       = "/"
	homeJSONDirPath   = filepath.Join(homeDirPath, ".docker")
	rootJSONDirPath   = filepath.Join(rootDirPath, ".docker")

	configFileName     = ".dockercfg"
	configJSONFileName = "config.json"
)

// SetPreferredDockercfgPath set preferred docker config path
func SetPreferredDockercfgPath(path string) {
	preferredPathLock.Lock()
	defer preferredPathLock.Unlock()
	preferredPath = path
}

// GetPreferredDockercfgPath get preferred docker config path
func GetPreferredDockercfgPath() string {
	preferredPathLock.Lock()
	defer preferredPathLock.Unlock()
	return preferredPath
}

// DefaultDockercfgPaths returns default search paths of .dockercfg
func DefaultDockercfgPaths() []string {
	return []string{GetPreferredDockercfgPath(), workingDirPath, homeDirPath, rootDirPath}
}

// DefaultDockerConfigJSONPaths returns default search paths of .docker/config.json
func DefaultDockerConfigJSONPaths() []string {
	return []string{GetPreferredDockercfgPath(), workingDirPath, homeJSONDirPath, rootJSONDirPath}
}

// ReadDockercfgFile attempts to read a legacy dockercfg file from the given paths.
// if searchPaths is empty, the default paths are used.
func ReadDockercfgFile(searchPaths []string) (cfg DockerConfig, err error) {
	if len(searchPaths) == 0 {
		searchPaths = DefaultDockercfgPaths()
	}

	for _, configPath := range searchPaths {
		absDockerConfigFileLocation, err := filepath.Abs(filepath.Join(configPath, configFileName))
		if err != nil {
			continue
		}
		contents, err := ioutil.ReadFile(absDockerConfigFileLocation)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			continue
		}
		cfg, err := readDockerConfigFileFromBytes(contents)
		if err != nil {
			continue
		}

		return cfg, nil

	}
	return nil, fmt.Errorf("couldn't find valid .dockercfg after checking in %v", searchPaths)
}

// ReadDockerConfigJSONFile attempts to read a docker config.json file from the given paths.
// if searchPaths is empty, the default paths are used.
func ReadDockerConfigJSONFile(searchPaths []string) (cfg DockerConfig, err error) {
	if len(searchPaths) == 0 {
		searchPaths = DefaultDockerConfigJSONPaths()
	}
	for _, configPath := range searchPaths {
		absDockerConfigFileLocation, err := filepath.Abs(filepath.Join(configPath, configJSONFileName))
		if err != nil {
			continue
		}
		cfg, err = ReadSpecificDockerConfigJSONFile(absDockerConfigFileLocation)
		if err != nil {
			if !os.IsNotExist(err) {
			}
			continue
		}
		return cfg, nil
	}
	return nil, fmt.Errorf("couldn't find valid %s after checking in %v", configJSONFileName, searchPaths)

}

// ReadSpecificDockerConfigJSONFile attempts to read docker configJSON from a given file path.
func ReadSpecificDockerConfigJSONFile(filePath string) (cfg DockerConfig, err error) {
	var contents []byte

	if contents, err = os.ReadFile(filePath); err != nil {
		return nil, err
	}

	cfg, err = ReadDockerConfigJSONFileFromBytes(contents)
	if err != nil {
		return nil, fmt.Errorf("invalid config: %q: %w", filePath, err)
	}
	return cfg, nil
}

func ReadDockerConfigJSONFileFromBytes(contents []byte) (cfg DockerConfig, err error) {
	var cfgJSON DockerConfigJSON
	if err = json.Unmarshal(contents, &cfgJSON); err != nil {
		return nil, fmt.Errorf("error occurred while trying to unmarshal json: %w", err)
	}
	cfg = cfgJSON.Auths
	return
}

// dockerConfigEntryWithAuth is used solely for deserializing the Auth field
// into a dockerConfigEntry during JSON deserialization.
type dockerConfigEntryWithAuth struct {
	// +optional
	Username string `json:"username,omitempty"`
	// +optional
	Password string `json:"password,omitempty"`
	// +optional
	Email string `json:"email,omitempty"`
	// +optional
	Auth string `json:"auth,omitempty"`
}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (ident *DockerConfigEntry) UnmarshalJSON(data []byte) error {
	var tmp dockerConfigEntryWithAuth
	err := json.Unmarshal(data, &tmp)
	if err != nil {
		return err
	}

	ident.Username = tmp.Username
	ident.Password = tmp.Password
	ident.Email = tmp.Email

	if len(tmp.Auth) == 0 {
		return nil
	}

	ident.Username, ident.Password, err = decodeDockerConfigFieldAuth(tmp.Auth)
	return err
}

// decodeDockerConfigFieldAuth deserializes the "auth" field from dockercfg into a
// username and a password. The format of the auth field is base64(<username>:<password>).
func decodeDockerConfigFieldAuth(field string) (username, password string, err error) {

	var decoded []byte

	// StdEncoding can only decode padded string
	// RawStdEncoding can only decode unpadded string
	if strings.HasSuffix(strings.TrimSpace(field), "=") {
		// decode padded data
		decoded, err = base64.StdEncoding.DecodeString(field)
	} else {
		// decode unpadded data
		decoded, err = base64.RawStdEncoding.DecodeString(field)
	}

	if err != nil {
		return
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		err = fmt.Errorf("unable to parse auth field, must be formatted as base64(username:password)")
		return
	}

	username = parts[0]
	password = parts[1]

	return
}

func readDockerConfigFileFromBytes(contents []byte) (cfg DockerConfig, err error) {
	if err = json.Unmarshal(contents, &cfg); err != nil {
		return nil, errors.New("error occurred while trying to unmarshal json")
	}
	return
}
