package proxy

import (
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func SetEnvVars(env *[]corev1.EnvVar, additionalNoProxy ...string) {
	setEnvVars(env, os.Getenv, additionalNoProxy...)
}

func setEnvVars(env *[]corev1.EnvVar, envGetter func(string) string, additionalNoProxy ...string) {
	var noProxy string
	httpProxy := envGetter("HTTP_PROXY")
	httpsProxy := envGetter("HTTPS_PROXY")
	if httpProxy != "" || httpsProxy != "" {
		// De-duplicate and sort
		additionalNoProxy = append(additionalNoProxy, "kube-apiserver")
		if envVal := envGetter("NO_PROXY"); envVal != "" {
			additionalNoProxy = append(additionalNoProxy, strings.Split(envVal, ",")...)
		}
		s := sets.NewString(additionalNoProxy...)
		noProxy = strings.Join(s.List(), ",")
	}
	SetEnvVarsTo(env,
		httpProxy,
		httpsProxy,
		noProxy,
	)
}

func SetEnvVarsTo(env *[]corev1.EnvVar, httpProxy, httpsProxy, noProxy string) {
	if httpProxy == "" {
		removeEnvVarIfPresent(env, "HTTP_PROXY")
	} else {
		upsertEnvVar(env, corev1.EnvVar{Name: "HTTP_PROXY", Value: httpProxy})
	}

	if httpsProxy == "" {
		removeEnvVarIfPresent(env, "HTTPS_PROXY")
	} else {
		upsertEnvVar(env, corev1.EnvVar{Name: "HTTPS_PROXY", Value: httpsProxy})
	}

	if noProxy == "" {
		removeEnvVarIfPresent(env, "NO_PROXY")
	} else {
		upsertEnvVar(env, corev1.EnvVar{Name: "NO_PROXY", Value: noProxy})
	}

}

func removeEnvVarIfPresent(list *[]corev1.EnvVar, name string) {
	for idx, envVar := range *list {
		if envVar.Name == name {
			*list = append((*list)[:idx], (*list)[idx+1:]...)
			return
		}
	}
}

func upsertEnvVar(list *[]corev1.EnvVar, envVar corev1.EnvVar) {
	for idx := range *list {
		if (*list)[idx].Name == envVar.Name {
			(*list)[idx].Value = envVar.Value
			return
		}
	}

	*list = append(*list, envVar)
}
