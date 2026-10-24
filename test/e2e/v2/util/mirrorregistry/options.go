package mirrorregistry

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
)

var sha256DigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func (o Options) withDefaults() Options {
	if o.Namespace == "" {
		o.Namespace = DefaultNamespace
	}
	if o.Name == "" {
		o.Name = DefaultName
	}
	if o.Image == "" {
		o.Image = DefaultRegistryImage
	}
	if o.Storage.Size.IsZero() {
		o.Storage.Size = resource.MustParse(DefaultStorageSize)
	}
	return o
}

func (o Options) validate() error {
	var problems []error
	for _, field := range []struct{ name, value string }{{"Name", o.Name}, {"Namespace", o.Namespace}} {
		if details := validation.IsDNS1123Label(field.value); len(details) > 0 {
			problems = append(problems, fmt.Errorf("%s %q is not a valid DNS-1123 label: %s", field.name, field.value, strings.Join(details, "; ")))
		}
	}
	if o.Copier == nil && o.CopierImage == "" {
		problems = append(problems, errors.New("CopierImage is required when Copier is nil"))
	}
	if o.CA != nil {
		if len(o.CA.CertPEM) == 0 {
			problems = append(problems, errors.New("CA.CertPEM must not be empty when CA is provided"))
		}
		if len(o.CA.KeyPEM) == 0 {
			problems = append(problems, errors.New("CA.KeyPEM must not be empty when CA is provided"))
		}
	}
	return errors.Join(problems...)
}

func routeHost(name, namespace, ingressDomain string) (string, error) {
	firstLabel := name + "-" + namespace
	if len(firstLabel) > 63 {
		return "", fmt.Errorf("route host first label %q exceeds 63 characters", firstLabel)
	}
	if ingressDomain == "" {
		return "", errors.New("ingress domain must not be empty")
	}
	return firstLabel + "." + ingressDomain, nil
}

func internalHost(name, namespace string) string {
	host := fmt.Sprintf("%s.%s.svc", name, namespace)
	return net.JoinHostPort(host, strconv.Itoa(RegistryPort))
}

func serviceDNSNames(name, namespace string) []string {
	service := name + "." + namespace
	return []string{name, service, service + ".svc", service + ".svc.cluster.local"}
}

func mirrorRepository(host, sourceRepository string) (string, error) {
	if host == "" {
		return "", errors.New("mirror host must not be empty")
	}
	if strings.Contains(sourceRepository, "@") {
		return "", fmt.Errorf("source repository %q must not contain a digest", sourceRepository)
	}
	parts := strings.Split(sourceRepository, "/")
	if len(parts) < 2 || parts[0] == "" {
		return "", fmt.Errorf("source repository %q must include a registry host and path", sourceRepository)
	}
	registryHost := parts[0]
	if !strings.ContainsAny(registryHost, ".:") && registryHost != "localhost" {
		return "", fmt.Errorf("source repository %q has no registry host", sourceRepository)
	}
	if strings.Contains(registryHost, ":") {
		if strings.Count(registryHost, ":") != 1 {
			return "", fmt.Errorf("source registry host %q must not use IPv6", registryHost)
		}
		registryHost = strings.Replace(registryHost, ":", "-", 1)
	}
	for _, component := range parts[1:] {
		if component == "" {
			return "", fmt.Errorf("source repository %q has an empty path component", sourceRepository)
		}
		if strings.Contains(component, ":") {
			return "", fmt.Errorf("source repository %q must not contain a tag", sourceRepository)
		}
	}
	return host + "/" + registryHost + "/" + strings.Join(parts[1:], "/"), nil
}

func splitDigestRef(ref string) (repository, digest string, err error) {
	if strings.Count(ref, "@") != 1 {
		return "", "", fmt.Errorf("image reference %q must contain exactly one digest separator", ref)
	}
	parts := strings.SplitN(ref, "@", 2)
	if parts[0] == "" || !sha256DigestPattern.MatchString(parts[1]) {
		return "", "", fmt.Errorf("image reference %q must use a sha256 digest with 64 hexadecimal characters", ref)
	}
	return parts[0], parts[1], nil
}
