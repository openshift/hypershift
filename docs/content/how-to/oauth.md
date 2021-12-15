# OAuth configuration

The Oauth configuration section allows a user to specify the desired Oauth configuration for the internal Openshift Oauth server. 
Guest cluster kube admin password will be exposed only when user has not explicitly specified the OAuth configuration. An example 
configuration for an openID identity provider is shown below:
```
apiVersion: hypershift.openshift.io/v1alpha1
kind: HostedCluster
metadata:
  name: example
  namespace: master
spec:
  configuration:
    items:
    - apiVersion: config.openshift.io/v1
      kind: OAuth
      metadata:
        name: "example"
      spec:
        identityProviders:
        - openID:
            claims:
              email:
                - email
              name:
                - name
              preferredUsername:
                - preferred_username
            clientID: clientid1
            clientSecret:
              name: clientid1-secret-name
            issuer: https://example.com/identity
          mappingMethod: lookup
          name: IAM
          type: OpenID
    secretRefs:
    - name: "clientid1-secret-name"
```

For more details on the individual identity providers: refer to [upstream openshift documentation](https://docs.openshift.com/container-platform/4.9/authentication/understanding-identity-provider.html)