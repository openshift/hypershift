package render

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"unicode"

	"github.com/blang/semver"
	"github.com/vincent-petithory/dataurl"
	"gopkg.in/ini.v1"

	corev1 "k8s.io/api/core/v1"
)

func imageFunc(images map[string]string) func(string) string {
	return func(imageName string) string {
		return images[imageName]
	}
}

func versionFunc(versions map[string]string) func(string) string {
	return func(component string) string {
		return versions[component]
	}
}

func lessThanVersionFunc(versions map[string]string) func(string) bool {
	return versionCompareFunc(versions, func(a, e semver.Version) bool { return a.LT(e) })
}

func atLeastVersionFunc(versions map[string]string) func(string) bool {
	return versionCompareFunc(versions, func(a, e semver.Version) bool { return a.GTE(e) })
}

func versionCompareFunc(versions map[string]string, versionCompareFn func(actual, expected semver.Version) bool) func(string) bool {
	releaseVersionStr := versions["release"]
	releaseVersion, err := semver.Parse(releaseVersionStr)
	if err != nil {
		panic(fmt.Sprintf("failed to parse release version %s: %v", releaseVersionStr, err))
	}
	releaseVersion.Pre = nil
	releaseVersion.Build = nil
	return func(version string) bool {
		parsedVersion, err := semver.Parse(version)
		if err != nil {
			panic(fmt.Sprintf("failed to parse version %s: %v", version, err))
		}
		return versionCompareFn(releaseVersion, parsedVersion)
	}
}

func pkiFunc(secrets *corev1.SecretList, configMaps *corev1.ConfigMapList) func(string, string, string) string {
	return func(resourceType, name, key string) string {
		b := findPKIData(resourceType, name, key, secrets, configMaps)
		return base64.StdEncoding.EncodeToString(b)
	}
}

func findPKIData(resourceType, name, key string, secrets *corev1.SecretList, configMaps *corev1.ConfigMapList) []byte {
	switch resourceType {
	case "secret":
		return getSecretData(secrets, name, key)
	case "configmap":
		return getConfigMapData(configMaps, name, key)
	default:
		panic("wrong resource type")
	}
}

func getSecretData(secrets *corev1.SecretList, name, key string) []byte {
	for _, secret := range secrets.Items {
		if secret.Name == name {
			if _, ok := secret.Data[key]; !ok {
				panic(fmt.Sprintf("key %s not found in secret %s", key, name))
			}
			return secret.Data[key]
		}
	}
	panic(fmt.Sprintf("secret %s not found", name))
}

func getConfigMapData(cms *corev1.ConfigMapList, name, key string) []byte {
	for _, cm := range cms.Items {
		if cm.Name == name {
			if _, ok := cm.Data[key]; !ok {
				panic(fmt.Sprintf("key %s not found in configmap %s", key, name))
			}
			return []byte(cm.Data[key])
		}
	}
	panic(fmt.Sprintf("configmap %s not found", name))
}

func includePKIFunc(secrets *corev1.SecretList, configMaps *corev1.ConfigMapList) func(string, string, string, int) string {
	return func(resourceType, name, key string, indent int) string {
		b := findPKIData(resourceType, name, key, secrets, configMaps)
		input := bytes.NewBuffer(b)
		output := &bytes.Buffer{}
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			fmt.Fprintf(output, "%s%s\n", strings.Repeat(" ", indent), scanner.Text())
		}
		return output.String()
	}
}

func pullSecretBase64(data []byte) func() string {
	return func() string {
		return base64.StdEncoding.EncodeToString(data)
	}
}

func base64Func(params interface{}, rc *renderContext) func(string) string {
	return func(fileName string) string {
		result, err := rc.substituteParams(params, fileName)
		if err != nil {
			panic(err.Error())
		}
		return base64.StdEncoding.EncodeToString([]byte(result))
	}
}

func includeDataFunc() func(string, int) string {
	return func(data string, indent int) string {
		input := bytes.NewBufferString(data)
		output := &bytes.Buffer{}
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			fmt.Fprintf(output, "%s%s\n", strings.Repeat(" ", indent), scanner.Text())
		}
		return output.String()
	}
}

func includeFileFunc(params interface{}, rc *renderContext) func(string, int) string {
	return func(fileName string, indent int) string {
		result, err := rc.substituteParams(params, fileName)
		if err != nil {
			panic(err.Error())
		}
		includeFn := includeDataFunc()
		return includeFn(string(result), indent)
	}
}

func dataURLEncode(params interface{}, rc *renderContext) func(string) string {
	return func(fileName string) string {
		result, err := rc.substituteParams(params, fileName)
		if err != nil {
			panic(err.Error())
		}
		return dataurl.EncodeBytes([]byte(result))
	}
}

func cidrAddress(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err.Error())
	}
	return ip.String()
}

func cidrMask(cidr string) string {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err.Error())
	}
	m := ipNet.Mask
	if len(m) != 4 {
		panic("Expecting a 4-byte mask")
	}
	return fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
}

func dnsForCidr(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err.Error())
	}
	dnsAddr := ip.To4()
	// dns for openshift mounts at x.x.x.10 in the service range by default
	dnsAddr[3] = 10
	return dnsAddr.String()
}

func iniValue(iniContent, section, key string) string {
	f, err := ini.Load([]byte(iniContent))
	if err != nil {
		panic(err.Error())
	}
	s, err := f.GetSection(section)
	if err != nil {
		panic(err.Error())
	}
	k, err := s.GetKey(key)
	if err != nil {
		panic(err.Error())
	}
	return k.String()
}

// randomString uses RawURLEncoding to ensure we do not get / characters or trailing ='s
func randomString(size int) string {
	// each byte (8 bits) gives us 4/3 base64 (6 bits) characters
	// we account for that conversion and add one to handle truncation
	b64size := base64.RawURLEncoding.DecodedLen(size) + 1
	// trim down to the original requested size since we added one above
	return base64.RawURLEncoding.EncodeToString(randomBytes(b64size))[:size]
}

func randomBytes(size int) []byte {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic(err) // rand should never fail
	}
	return b
}

func indent(spaces int, v string) string {
	pad := strings.Repeat(" ", spaces)
	return pad + strings.Replace(v, "\n", "\n"+pad, -1)
}

func base64StringEncode(inputString string) string {
	return base64.StdEncoding.EncodeToString([]byte(inputString))
}

func trimTrailingSpace(s string) string {
	return strings.TrimRightFunc(s, unicode.IsSpace)
}
