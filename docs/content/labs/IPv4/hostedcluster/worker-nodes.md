Regarding the worker nodes, if you are working on real bare metal, this step is crucial to ensure that the details set in the `BareMetalHost` are correctly configured. If not, you will need to debug why it's not functioning as expected.

However, if you are working with virtual machines, you can follow these steps to create empty ones that will be consumed by the Metal3 operator. To achieve this, we will utilize Kcli.

## Creating Virtual Machines

If this is not your first attempt, you must first delete the previous setup. To do so, please refer to the [Deleting Virtual Machines](#deleting-virtual-machines) section.

Now, you can execute the following commands for VM creation:

```bash
kcli create vm -P start=False -P uefi_legacy=true -P plan=hosted-ipv4 -P memory=8192 -P numcpus=16 -P disks=[200,200] -P nets=["{\"name\": \"ipv4\", \"mac\": \"aa:aa:aa:aa:02:11\"}"] -P uuid=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa0211 -P name=hosted-ipv4-worker0
kcli create vm -P start=False -P uefi_legacy=true -P plan=hosted-ipv4 -P memory=8192 -P numcpus=16 -P disks=[200,200] -P nets=["{\"name\": \"ipv4\", \"mac\": \"aa:aa:aa:aa:02:12\"}"] -P uuid=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa0212 -P name=hosted-ipv4-worker1
kcli create vm -P start=False -P uefi_legacy=true -P plan=hosted-ipv4 -P memory=8192 -P numcpus=16 -P disks=[200,200] -P nets=["{\"name\": \"ipv4\", \"mac\": \"aa:aa:aa:aa:02:13\"}"] -P uuid=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa0213 -P name=hosted-ipv4-worker2

sleep 2
systemctl restart ksushy
```

Let's dissect the creation command:

- `start=False`: The VM will not boot automatically upon creation.
- `uefi_legacy=true`: We will use UEFI legacy boot to ensure compatibility with older UEFI implementations.
- `plan=hosted-dual`: The plan name, which identifies a group of machines as a cluster.
- `memory=8192` and `numcpus=16`: These parameters specify the resources for the VM, including RAM and CPU.
- `disks=[200,200]`: We are creating 2 disks (thin provisioned) in the virtual machine.
- `nets=[{"name": "dual", "mac": "aa:aa:aa:aa:02:13"}]`: Network details, including the network name it will be connected to and the MAC address for the primary interface.
- The `ksushy` restart is performed to make our `ksushy` (VM's BMC) aware of the new VMs added.

This is what the command looks like:

```bash
+---------------------+--------+-------------------+----------------------------------------------------+-------------+---------+
|         Name        | Status |         Ip        |                       Source                       |     Plan    | Profile |
+---------------------+--------+-------------------+----------------------------------------------------+-------------+---------+
|    hosted-worker0   |  down  |                   |                                                    | hosted-ipv4 |  kvirt  |
|    hosted-worker1   |  down  |                   |                                                    | hosted-ipv4 |  kvirt  |
|    hosted-worker2   |  down  |                   |                                                    | hosted-ipv4 |  kvirt  |
+---------------------+--------+-------------------+----------------------------------------------------+-------------+---------+
```

## Deleting Virtual Machines

To delete the VMs, you simply need to delete the plan, which, in our case, is:

```bash
kcli delete plan hosted-ipv4
```

```bash
$ kcli delete plan hosted-ipv4
Are you sure? [y/N]: y
hosted-worker0 deleted on local!
hosted-worker1 deleted on local!
hosted-worker2 deleted on local!
Plan hosted-ipv4 deleted!
```