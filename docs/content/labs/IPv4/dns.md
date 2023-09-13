The DNS configuration is a critical aspect of our setup. To enable name resolution in our virtualized environment, follow these steps:

1. Create the primary DNS configuration file for the dnsmasq server:

- `/opt/dnsmasq/dnsmasq.conf`
```conf
strict-order
bind-dynamic
#log-queries
bogus-priv
dhcp-authoritative

dhcp-range=ipv4,192.168.125.120,192.168.125.250,255.255.255.0,24h
dhcp-option=ipv4,option:dns-server,192.168.125.1
dhcp-option=ipv4,option:router,192.168.125.1

resolv-file=/opt/dnsmasq/upstream-resolv.conf
except-interface=lo
dhcp-lease-max=81
log-dhcp
no-hosts

# DHCP Reservations
dhcp-leasefile=/opt/dnsmasq/hosts.leases

# Include all files in a directory depending on the suffix
conf-dir=/opt/dnsmasq/include.d/*.ipv4
```

Create the upstream resolver to delegate the non-local environments queries

- `/opt/dnsmasq/upstream-resolv.conf`
```
nameserver 8.8.8.8
nameserver 8.8.4.4
```

Create the different component DNS configurations

- `/opt/dnsmasq/include.d/hosted-nodeport.ipv4`
```
## Nodeport
host-record=api-int.hosted-ipv4.hypershiftbm.lab,192.168.125.20
host-record=api-int.hosted-ipv4.hypershiftbm.lab,192.168.125.21
host-record=api-int.hosted-ipv4.hypershiftbm.lab,192.168.125.22
host-record=api.hosted-ipv4.hypershiftbm.lab,192.168.125.20
host-record=api.hosted-ipv4.hypershiftbm.lab,192.168.125.21
host-record=api.hosted-ipv4.hypershiftbm.lab,192.168.125.22

## Nodeport
## IMPORTANT!: You should point to the node which is exposing the router.
## You can also use MetalLB to expose the Apps wildcard.
address=/apps.hosted-ipv4.hypershiftbm.lab/192.168.125.30

## General
dhcp-host=aa:aa:aa:aa:02:01,hosted-ipv4-worker0,192.168.125.30
dhcp-host=aa:aa:aa:aa:02:02,hosted-ipv4-worker1,192.168.125.31
dhcp-host=aa:aa:aa:aa:02:03,hosted-ipv4-worker2,192.168.125.32
```

- `/opt/dnsmasq/include.d/hub.ipv4`
```
host-record=api-int.hub-ipv4.hypershiftbm.lab,192.168.125.10
host-record=api.hub-ipv4.hypershiftbm.lab,192.168.125.10
address=/apps.hub-ipv4.hypershiftbm.lab/192.168.125.11
dhcp-host=aa:aa:aa:aa:02:01,ocp-master-0,192.168.125.20
dhcp-host=aa:aa:aa:aa:02:02,ocp-master-1,192.168.125.21
dhcp-host=aa:aa:aa:aa:02:03,ocp-master-2,192.168.125.22
dhcp-host=aa:aa:aa:aa:02:06,ocp-installer,192.168.125.25
dhcp-host=aa:aa:aa:aa:02:10,ocp-bootstrap,192.168.125.26
```

- `/opt/dnsmasq/include.d/infra.ipv4`
```
host-record=registry.hypershiftbm.lab,192.168.125.1
```

To proceed, we must create a systemd service for the management of the dnsmasq service and disable the system's default dnsmasq service:

- `/etc/systemd/system/dnsmasq-virt.service`
```
[Unit]
Description=DNS server for Openshift 4 Clusters.
After=network.target

[Service]
User=root
Group=root
ExecStart=/usr/sbin/dnsmasq -k --conf-file=/opt/dnsmasq/dnsmasq.conf

[Install]
WantedBy=multi-user.target
```

The commands to do so:

```
systemctl daemon-reload
systemctl disable --now dnsmasq
systemctl enable --now dnsmasq-virt
```

!!! note

    This step is mandatory for both Disconnected and Connected environments. Additionally, it holds significance for both Virtualized and Bare Metal environments. The key distinction lies in the location where the resources will be configured. In a non-virtualized environment, a more robust solution like Bind is recommended instead of a lightweight dnsmasq.