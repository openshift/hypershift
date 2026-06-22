## Configure MetalLB for HostedCluster's Data Plane

- Deploy the MetalLB Operator using the OLM, applying this manifest or using the UI Console:

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: metallb-operator
  namespace: openshift-operators
spec:
  channel: "stable"
  name: metallb-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
```

- Deploy the MetalLB CR:

```yaml
---
apiVersion: metallb.io/v1beta1
kind: MetalLB
metadata:
  name: metallb
  namespace: openshift-operators
```

This deploys the metallb-controller-manager and the webhook-server.

- Configure the IPAddressPool and the L2Advertisement:

```yaml
---
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: lab-network
  namespace: openshift-operators
spec:
  autoAssign: true
  addresses:
  - 192.168.126.160-192.168.126.165
  - 2620:52:0:1306::160-2620:52:0:1306::169
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: advertise-lab-network
  namespace: openshift-operators
spec:
  ipAddressPools:
  - lab-network
```

!!! Note
    The sample config is based on a DualStack layout. If your deployment uses only one stack, specify the IPAddressPool for that stack.

- Expose the OpenShift service. This is usually done in both the Control Plane for MGMT configuration and the Data Plane to configure the Ingress:

```yaml
kind: Service
apiVersion: v1
metadata:
  annotations:
    metallb.universe.tf/address-pool: lab-network
  name: metallb-ingress
  namespace: openshift-ingress
spec:
  ipFamilies:
  - IPv4
  - IPv6
  ipFamilyPolicy: PreferDualStack
  ports:
    - name: http
      protocol: TCP
      port: 80
      targetPort: 80
    - name: https
      protocol: TCP
      port: 443
      targetPort: 443
  selector:
    ingresscontroller.operator.openshift.io/deployment-ingresscontroller: default
  type: LoadBalancer
```

!!! Note
    The sample config is based on a DualStack layout. If your deployment uses only one stack, specify the ipFamilies for that stack and modify the ipFamilyPolicy accordingly.

The usual configuration for the Hosted Cluster in the BareMetal case is a mix between Route and LoadBalancer strategies:

```yaml
spec:
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
  - service: OAuthServer
    servicePublishingStrategy:
      type: Route
  - service: OIDC
    servicePublishingStrategy:
      type: Route
      Route:
        hostname: <URL>
  - service: Konnectivity
    servicePublishingStrategy:
      type: Route
  - service: Ignition
    servicePublishingStrategy:
      type: Route
```

This way, the API server is configured as a LoadBalancer, and the rest of the services are exposed via Route.

!!! Note
    You can specify a specific URL to expose the service you want.