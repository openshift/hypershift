 Hosted Cluster, as mentioned in the documentation [here](https://hypershift.pages.dev/reference/concepts-and-personas/), is essentially an OCP API endpoint managed by Hypershift. In this context, we will also include the term HostedControlPlane to enhance readability and comprehension. This terminology is further explained in the same [link](https://hypershift.pages.dev/reference/concepts-and-personas/).

The Hosted Cluster consists of two main components:
- The Control Plane, which operates within the management cluster as pods.
- The Data Plane, which comprises external nodes managed by the end user.

Now, with this foundational understanding, we can proceed with the deployment of our Hosted Cluster. While ACM/MCE users typically employ the web UI for cluster creation, we will take advantage of manifests, which provide greater flexibility for modifying the artifacts.
