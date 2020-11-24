# Hypershift POC

All the following assumes `KUBECONFIG` points to the management cluster.

Build binaries: 

```
$ make
```

Install the operator's supporting resources into the management cluster:
```
$ oc apply --filename manifests/
```

Define release image info to be referenced by clusters:

```
hack/generate-release-images.rb | oc apply --filename -
```

Run the operator:
```
$ bin/hypershift-operator run --control-plane-operator-image quay.io/hypershift/hypershift:latest
```

Run the sigs.k8s.io cluster API controller:
```
$ git clone -b set-providerID git@github.com:enxebre/cluster-api.git
$ cd cluster-api && make manager-core
$ ./bin/manager
```

Export your AWS to environment variables and run the sigs.k8s.io cluster API AWS controller:
```
$ cd ~/go/src/sigs.k8s.io/
$ git clone -b decouple-machine-infra git@github.com:enxebre/cluster-api-provider-aws-2.git cluster-api-provider-aws
cd cluster-api-provider-aws && make manager-aws-infrastructure
./bin/manager --alsologtostderr --metrics-addr=0 --health-addr=0
```

Create a cluster, referencing a release image present in the `release-images` configmap
previously created:

```yaml
apiVersion: hypershift.openshift.io/v1alpha1
kind: OpenShiftCluster
metadata:
  namespace: hypershift
  name: guest-hello
spec:
  releaseImage: quay.io/openshift-release-dev/ocp-release@sha256:d78292e9730dd387ff6198197c8b0598da340be7678e8e1e4810b557a926c2b9
  baseDomain: guest-hello.devcluster.openshift.com
  pullSecret: '{"auths": { ... }}'
  serviceCIDR: 172.31.0.0/16
  podCIDR: 10.132.0.0/14
  sshKey: 'ssh-rsa ...'
  initialComputeReplicas: 1
```
Create additional nodePools
```yaml
apiVersion: hypershift.openshift.io/v1alpha1
kind: NodePool
metadata:
  name: guest-hello-custom-nodepool
  namespace: hypershift
spec:
  clusterName: guest-hello
  autoScaling:
    max: 0
    min: 0
  nodeCount: 1
  platform:
    aws:
      instanceType: m5.large
```
Get the cluster kubeconfig using:
```
$ oc get secret --namespace hypershift guest-hello-kubeconfig --template={{.data.value}} | base64 -D
```

And delete the cluster using:

```
$ oc delete --namespace hypershift openshiftclusters/guest-hello
```
