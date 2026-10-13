Firstly, we need to ensure that we have the right networks prepared for use in the Hypervisor. These networks will be used to host both the Management and Hosted clusters.

To configure these networks, we will use the following `kcli` command:

```
kcli create network -c 192.168.125.0/24 -P dhcp=false -P dns=false --domain hypershiftbm.lab ipv4
```

Where:

- `-c` specifies the CIDR used for that network.
- `-P dhcp=false` configures the network to disable DHCP, which will be handled by the previously configured dnsmasq.
- `-P dns=false` configures the network to disable DNS, which will also be handled by the dnsmasq.
- `--domain` sets the domain to search into.
- `ipv4` is the name of the network that will be created.

This is what the network will look like once created:

```
[root@hypershiftbm ~]# kcli list network
Listing Networks...
+---------+--------+---------------------+-------+------------------+------+
| Network |  Type  |         Cidr        |  Dhcp |      Domain      | Mode |
+---------+--------+---------------------+-------+------------------+------+
| default | routed |   192.168.122.0/24  |  True |     default      | nat  |
| ipv4    | routed |   192.168.125.0/24  | False | hypershiftbm.lab | nat  |
| ipv6    | routed | 2620:52:0:1306::/64 | False | hypershiftbm.lab | nat  |
+---------+--------+---------------------+-------+------------------+------+
```

```
[root@hypershiftbm ~]# kcli info network ipv4
Providing information about network ipv4...
cidr: 192.168.125.0/24
dhcp: false
domain: hypershiftbm.lab
mode: nat
plan: kvirt
type: routed
```

