The DNS configuration is a critical aspect of our setup. To enable name resolution in our virtualized environment, follow these steps:

1. Create the primary DNS configuration file for the dnsmasq server:

- `/opt/dnsmasq/dnsmasq.conf`
```conf
strict-order
bind-dynamic
#log-queries
bogus-priv
dhcp-authoritative

# BM Network IPv6
dhcp-range=ipv6,2620:52:0:1305::11,2620:52:0:1305::20,64
dhcp-option=ipv6,option6:dns-server,2620:52:0:1305::1

resolv-file=/opt/dnsmasq/upstream-resolv.conf
except-interface=lo
dhcp-lease-max=81
log-dhcp
no-hosts

# DHCP Reservations
dhcp-leasefile=/opt/dnsmasq/hosts.leases

# Include all files in a directory depending on the suffix
conf-dir=/opt/dnsmasq/include.d/*.ipv6
```

Create the upstream resolver to delegate the non-local environments queries

- `/opt/dnsmasq/upstream-resolv.conf`
```
nameserver 8.8.8.8
nameserver 8.8.4.4
```

Create the different component DNS configurations

- `/opt/dnsmasq/include.d/hosted-nodeport.ipv6`
```
host-record=api-int.hosted-ipv6.hypershiftbm.lab,2620:52:0:1305::5
host-record=api-int.hosted-ipv6.hypershiftbm.lab,2620:52:0:1305::6
host-record=api-int.hosted-ipv6.hypershiftbm.lab,2620:52:0:1305::7
host-record=api.hosted-ipv6.hypershiftbm.lab,2620:52:0:1305::5
host-record=api.hosted-ipv6.hypershiftbm.lab,2620:52:0:1305::6
host-record=api.hosted-ipv6.hypershiftbm.lab,2620:52:0:1305::7
address=/apps.hosted-ipv6.hypershiftbm.lab/2620:52:0:1305::60
dhcp-host=aa:aa:aa:aa:04:11,hosted-worker0,[2620:52:0:1305::11]
dhcp-host=aa:aa:aa:aa:04:12,hosted-worker1,[2620:52:0:1305::12]
dhcp-host=aa:aa:aa:aa:04:13,hosted-worker2,[2620:52:0:1305::13]
```

- `/opt/dnsmasq/include.d/hub.ipv6`
```
host-record=api-int.hub-ipv6.hypershiftbm.lab,2620:52:0:1305::2
host-record=api.hub-ipv6.hypershiftbm.lab,2620:52:0:1305::2
address=/apps.hub-ipv6.hypershiftbm.lab/2620:52:0:1305::3
dhcp-host=aa:aa:aa:aa:03:01,ocp-master-0,[2620:52:0:1305::5]
dhcp-host=aa:aa:aa:aa:03:02,ocp-master-1,[2620:52:0:1305::6]
dhcp-host=aa:aa:aa:aa:03:03,ocp-master-2,[2620:52:0:1305::7]
dhcp-host=aa:aa:aa:aa:03:06,ocp-installer,[2620:52:0:1305::8]
dhcp-host=aa:aa:aa:aa:03:07,ocp-bootstrap,[2620:52:0:1305::9]
```

- `/opt/dnsmasq/include.d/infra.ipv6`
```
host-record=registry.hypershiftbm.lab,2620:52:0:1305::1
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