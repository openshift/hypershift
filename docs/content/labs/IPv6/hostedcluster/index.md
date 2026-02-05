 Hosted Cluster, as mentioned in the documentation [here](https://hypershift.pages.dev/reference/concepts-and-personas/), is essentially an OCP API endpoint managed by Hypershift. In this context, we will also include the term HostedControlPlane to enhance readability and comprehension. This terminology is further explained in the same [link](https://hypershift.pages.dev/reference/concepts-and-personas/).

The Hosted Cluster comprises two main components:
- The Control Plane, which runs as pods in the management cluster.
- The Data Plane, consisting of external nodes managed by the end user.

With this foundational understanding, we can commence our Hosted Cluster deployment. Typically, an ACM/MCE user would utilize the web UI to create a cluster. However, in this scenario, we will leverage manifests, providing us with greater flexibility to modify the artifacts.
