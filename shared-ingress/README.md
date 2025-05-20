# HAProxy v3 image for shared ingress in ARO/HCP

This subdirectory contains the necessary files to perform a hermetic build of an
HAProxy v3 image that we can use as a shared ingress router pod. Following best
practices, we pin both the base image and the rpm versions of the dependencies
we install. If you need to change them, do so and then regenerate the lock file
doing:

```shell
rpm-lockfile-prototype --outfile=shared-ingress/rpms.lock.yaml shared-ingress/rpms.in.yaml
```
