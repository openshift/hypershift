**Introduction**

This section elucidates the collaboration between Multicluster Engine and Agent to facilitate in-house deployments. Detailed documentation for each of the network stacks can be found in the *Self-Managed Laboratories* section. If you intend to set up a self-managed environment, please proceed to that section and follow the provided steps.

**High-Level Overview**

![Hypershift on Bare Metal](/images/diagram-hypershift-on-baremetal.png)

The diagram above provides an overview of the environment and how the workflow functions, along with labeled components for reference:

1. **(Applicable in Disconnected Environments Only)**: Create a ConfigMap with a specified name in the `openshift-config` namespace. In this example, we'll name it `registry-config`. The content of this ConfigMap should be the Registry CA certificate.
2. **(Applicable in Disconnected Environments Only)**: Modify the `images.config.openshift.io` Custom Resource (CR) and add a new field in the spec named `additionalTrustedCA`, with a value of `name: registry-config`.
3. **(Applicable in Disconnected Environments Only)**: Create a ConfigMap with a specified name containing two data fields. One field should contain the `registries.conf` file in RAW format, and the other should be named `ca-bundle.crt`, which should contain the Registry CA.
4. Create the `multiclusterengine` CR, enabling both the Agent and Hypershift addons.
5. Create the HostedCluster Objects. This involves several components, including:
    1. **Secrets**: These contain the *PullSecret*, *SSHKey*, and *ETCD Encryption Key*.
    2. **ConfigMap** **(Applicable in Disconnected Environments Only)**: This ConfigMap contains the CA certificate of the private registry.
    3. **HostedCluster**: Defines the configuration of the cluster you intend to create.
    4. **NodePool**: Identifies the pool that references the Machines to be used for the data plane.

6. Following the creation of HostedCluster Objects, the Hypershift Operator will establish the __HostedControlPlane Namespace__ to accommodate ControlPlane pods. This namespace will also host components like **Agents**, **BareMetalHosts**, **Infraenv**, and more. Subsequently, you need to create the **InfraEnv** and, after ISO creation, the BareMetalHosts along with their secrets containing BMC credentials.

7. The Metal3 operator, within the `openshift-machine-api` namespace, will inspect the newly created BareMetalHosts. It will then attempt to connect to the BMCs to boot them up using the configured __LiveISO__ and __RootFS__ specified through the **AgentServiceConfig** CR in the MCE namespace.

8. Once the worker nodes of the HostedCluster have successfully booted up, an __Agent__ container will be initiated. This __Agent__ will establish contact with the Assisted Service, which will orchestrate the necessary actions to complete the deployment. Initially, you will need to scale the NodePool to the desired number of worker nodes for your HostedCluster, after which the AssistedService will manage the remaining tasks.

9. At this point, you need to patiently await the completion of the deployment process.
