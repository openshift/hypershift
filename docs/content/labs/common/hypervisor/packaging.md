---
title: Hypervisor Packaging
---

## System

These are the main packages that are needed to deploy a virtualized Openshift Management cluster.

```bash
sudo dnf install dnsmasq radvd vim golang podman bind-utils net-tools httpd-tools tree htop strace tmux -y
```

Additionally, you need to enable and start the Podman service using the following command:

```bash
systemctl enable --now podman
```

## Kcli

We will utilize [Kcli](https://kcli.readthedocs.io/en/latest/) to deploy the Openshift Management cluster and various other virtualized components. To do so, you'll need to install and configure the hypervisor using the following commands:

```bash
sudo yum -y install libvirt libvirt-daemon-driver-qemu qemu-kvm
sudo usermod -aG qemu,libvirt $(id -un)
sudo newgrp libvirt
sudo systemctl enable --now libvirtd
sudo dnf -y copr enable karmab/kcli
sudo dnf -y install kcli
sudo kcli create pool -p /var/lib/libvirt/images default
kcli create host kvm -H 127.0.0.1 local
sudo setfacl -m u:$(id -un):rwx /var/lib/libvirt/images
kcli create network  -c 192.168.122.0/24 default
```

For more info about Kcli please visit [the official documentation](https://kcli.readthedocs.io).