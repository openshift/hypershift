In this section, we will discuss how to deploy the Openshift management cluster. To do that, we need to have the following files in place:

1. **Pull Secret**
2. **Kcli Plan**

Ensure that these files are properly set up for the deployment process.

### Pull Secret

The Pull Secret should be located in the same folder as the kcli plan and should be named `openshift_pull.json`.

### Kcli plan

The Kcli plan contains the Openshift definition. This is how looks like:

!!! note

    Please ensure you modify the appropriate fields to align with your laboratory environment.

- `mgmt-compact-hub-dual.yaml`

```yaml
plan: hub-dual
force: true
version: nightly
tag: "4.14.0-0.nightly-2023-08-29-102237"
cluster: "hub-dual"
dualstack: true
domain: hypershiftbm.lab
api_ip: 192.168.126.10
ingress_ip: 192.168.126.11
service_networks:
- 172.30.0.0/16
- fd02::/112
cluster_networks:
- 10.132.0.0/14
- fd01::/48
disconnected_url: registry.hypershiftbm.lab:5000
disconnected_update: true
disconnected_user: dummy
disconnected_password: dummy
disconnected_operators_version: v4.13
disconnected_operators:
- name: metallb-operator
- name: lvms-operator
  channels:
  - name: stable-4.13
disconnected_extra_images:
- quay.io/mavazque/trbsht:latest
- quay.io/jparrill/hypershift:BMSelfManage-v4.14-rc-v3
- registry.redhat.io/openshift4/ose-kube-rbac-proxy:v4.10
dualstack: true
disk_size: 200
extra_disks: [200]
memory: 48000
numcpus: 16
ctlplanes: 3
workers: 0
manifests: extra-manifests
metal3: true
network: dual
users_dev: developer
users_devpassword: developer
users_admin: admin
users_adminpassword: admin
metallb_pool: dual-virtual-network
metallb_ranges:
- 192.168.126.150-192.168.126.190
metallb_autoassign: true
apps:
- users
- lvms-operator
- metallb-operator
vmrules:
- hub-bootstrap:
    nets:
    - name: ipv6
      mac: aa:aa:aa:aa:10:07
- hub-ctlplane-0:
    nets:
    - name: ipv6
      mac: aa:aa:aa:aa:10:01
- hub-ctlplane-1:
    nets:
    - name: ipv6
      mac: aa:aa:aa:aa:10:02
- hub-ctlplane-2:
    nets:
    - name: ipv6
      mac: aa:aa:aa:aa:10:03
```

!!! note

    To understand the meaning of each of these parameters, you can refer to the official documentation [here](https://kcli.readthedocs.io/en/latest/#how-to-use).

### Deployment

To initiate the provisioning procedure, execute the following:

```bash
kcli create cluster openshift --pf mgmt-compact-hub-dual.yaml
```