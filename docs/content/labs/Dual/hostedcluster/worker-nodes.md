Regarding the worker nodes, if you are working on real bare metal, this step is crucial to ensure that the details set in the `BareMetalHost` are correctly configured. If not, you will need to debug why it's not functioning as expected.

However, if you are working with virtual machines, you can follow these steps to create empty ones that will be consumed by the Metal3 operator. To achieve this, we will utilize Kcli.

## Creating Virtual Machines

If this is not your first attempt, you must first delete the previous setup. To do so, please refer to the [Deleting Virtual Machines](#deleting-virtual-machines) section.

Now, you can execute the following commands for VM creation:

```bash
kcli create vm -P start=False -P uefi_legacy=true -P plan=hosted-dual -P memory=8192 -P numcpus=16 -P disks=[200,200] -P nets=["{\"name\": \"dual\", \"mac\": \"aa:aa:aa:aa:11:01\"}"] -P uuid=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa1101 -P name=hosted-dual-worker0
kcli create vm -P start=False -P uefi_legacy=true -P plan=hosted-dual -P memory=8192 -P numcpus=16 -P disks=[200,200] -P nets=["{\"name\": \"dual\", \"mac\": \"aa:aa:aa:aa:11:02\"}"] -P uuid=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa1102 -P name=hosted-dual-worker1
kcli create vm -P start=False -P uefi_legacy=true -P plan=hosted-dual -P memory=8192 -P numcpus=16 -P disks=[200,200] -P nets=["{\"name\": \"dual\", \"mac\": \"aa:aa:aa:aa:11:03\"}"] -P uuid=aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa1103 -P name=hosted-dual-worker2

sleep 2
systemctl restart ksushy
```

Let's dissect the creation command:

- `start=False`: The VM will not boot automatically upon creation.
- `uefi_legacy=true`: We will use UEFI legacy boot to ensure compatibility with older UEFI implementations.
- `plan=hosted-dual`: The plan name, which identifies a group of machines as a cluster.
- `memory=8192` and `numcpus=16`: These parameters specify the resources for the VM, including RAM and CPU.
- `disks=[200,200]`: We are creating 2 disks (thin provisioned) in the virtual machine.
- `nets=[{"name": "dual", "mac": "aa:aa:aa:aa:11:13"}]`: Network details, including the network name it will be connected to and the MAC address for the primary interface.
- The `ksushy` restart is performed to make our `ksushy` (VM's BMC) aware of the new VMs added.

This is what the command looks like:

```bash
+---------------------+--------+-------------------+----------------------------------------------------+-------------+---------+
|         Name        | Status |         Ip        |                       Source                       |     Plan    | Profile |
+---------------------+--------+-------------------+----------------------------------------------------+-------------+---------+
|    hosted-worker0   |  down  |                   |                                                    | hosted-dual |  kvirt  |
|    hosted-worker1   |  down  |                   |                                                    | hosted-dual |  kvirt  |
|    hosted-worker2   |  down  |                   |                                                    | hosted-dual |  kvirt  |
+---------------------+--------+-------------------+----------------------------------------------------+-------------+---------+
```

## Deleting Virtual Machines

To delete the VMs, you simply need to delete the plan, which, in our case, is:

```bash
kcli delete plan hosted-dual
```

```bash
$ kcli delete plan hosted-dual
Are you sure? [y/N]: y
hosted-worker0 deleted on local!
hosted-worker1 deleted on local!
hosted-worker2 deleted on local!
Plan hosted-dual deleted!
```