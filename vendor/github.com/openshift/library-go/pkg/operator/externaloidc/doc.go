// Package externaloidc generates oauth-apiserver External OIDC configuration
// from the cluster Authentication configuration. It is intended for operators
// such as cluster-authentication-operator and HyperShift, which provide the
// resource resolvers needed to read referenced ConfigMaps and Secrets.
package externaloidc
