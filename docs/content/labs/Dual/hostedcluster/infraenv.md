The `InfraEnv` is an Assisted Service object that includes essential details such as the `pullSecretRef` and the `sshAuthorizedKey`. These details are used to create the RHCOS Boot Image customized specifically for the cluster. Below is the structure of this object:

```yaml
---
apiVersion: agent-install.openshift.io/v1beta1
kind: InfraEnv
metadata:
  name: hosted-dual
  namespace: clusters-hosted-dual
spec:
  pullSecretRef:
    name: pull-secret
  sshAuthorizedKey: ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDk7ICaUE+/k4zTpxLk4+xFdHi4ZuDi5qjeF52afsNkw0w/glILHhwpL5gnp5WkRuL8GwJuZ1VqLC9EKrdmegn4MrmUlq7WTsP0VFOZFBfq2XRUxo1wrRdor2z0Bbh93ytR+ZsDbbLlGngXaMa0Vbt+z74FqlcajbHTZ6zBmTpBVq5RHtDPgKITdpE1fongp7+ZXQNBlkaavaqv8bnyrP4BWahLP4iO9/xJF9lQYboYwEEDzmnKLMW1VtCE6nJzEgWCufACTbxpNS7GvKtoHT/OVzw8ArEXhZXQUS1UY8zKsX2iXwmyhw5Sj6YboA8WICs4z+TrFP89LmxXY0j6536TQFyRz1iB4WWvCbH5n6W+ABV2e8ssJB1AmEy8QYNwpJQJNpSxzoKBjI73XxvPYYC/IjPFMySwZqrSZCkJYqQ023ySkaQxWZT7in4KeMu7eS2tC+Kn4deJ7KwwUycx8n6RHMeD8Qg9flTHCv3gmab8JKZJqN3hW1D378JuvmIX4V0=
```

**Details**:

- `pullSecretRef` refers to the ConfigMap reference (in the same Namespace as the InfraEnv) where the PullSecret will be utilized.
- `sshAuthorizedKey` represents the SSH Public key that will be injected into the Boot Image. This SSH key will, by default, allow access to the worker nodes as the `core` user.

To deploy this object, follow the same procedure as before:

```bash
oc apply -f 03-infraenv.yaml
```

Once deployed, it will appear as follows:

```bash
NAMESPACE              NAME     ISO CREATED AT
clusters-hosted-dual   hosted   2023-09-11T15:14:10Z
```