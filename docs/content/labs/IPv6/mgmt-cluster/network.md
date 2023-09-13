Firstly, we need to ensure that we have the right networks prepared for use in the Hypervisor. These networks will be used to host both the Management and Hosted clusters.

To configure these networks, we will use the following `kcli` command:

```
kcli create network -c 2620:52:0:1305::0/64 -P dhcp=false -P dns=false --domain hypershiftbm.lab --nodhcp ipv6
```

Where:

- `-c` remarks the CIDR used for that network
- `-P dhcp=false` configures the network to disable the DHCP, this will be done by the dnsmasq we've configured before.
- `-P dns=false` configures the network to disable the DNS, this will be done by the dnsmasq we've configured before.
- `--domain` sets the domain to search into.
- `ipv6` is the name of the network that will be created.

This is what the network will look like once created:

```
[root@hypershiftbm ~]# kcli list network
Listing Networks...
+---------+--------+---------------------+-------+------------------+------+
| Network |  Type  |         Cidr        |  Dhcp |      Domain      | Mode |
+---------+--------+---------------------+-------+------------------+------+
| default | routed |   192.168.122.0/24  |  True |     default      | nat  |
| ipv6    | routed |   192.168.125.0/24  | False | hypershiftbm.lab | nat  |
| ipv6    | routed | 2620:52:0:1305::/64 | False | hypershiftbm.lab | nat  |
+---------+--------+---------------------+-------+------------------+------+
```

```
[root@hypershiftbm ~]# kcli info network ipv6
Providing information about network ipv6...
cidr: 2620:52:0:1305::/64
dhcp: false
domain: hypershiftbm.lab
mode: nat
plan: kvirt
type: routed
```

