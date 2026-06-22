!!! important

    This section is only relevant in disconnected scenarios, if this is not your case, you can continue with the next section.

This section talks about an additional webserver that you need to configure to host the RHCOS images associated with the Openshift release you are trying to deploy as a HostedCluster.

The script refers to [this repository folder](https://github.com/jparrill/hypershift-disconnected/tree/main/assets/ipv4/05-webserver) and it's the same for all three different network stacks.

To do this, you can use this script:

```bash
#!/bin/bash

WEBSRV_FOLDER=/opt/srv
ROOTFS_IMG_URL="$(../04-management-cluster/openshift-install coreos print-stream-json | jq -r '.architectures.x86_64.artifacts.metal.formats.pxe.rootfs.location')"
LIVE_ISO_URL="$(../04-management-cluster/openshift-install coreos print-stream-json | jq -r '.architectures.x86_64.artifacts.metal.formats.iso.disk.location')"

mkdir -p ${WEBSRV_FOLDER}/images
curl -Lk ${ROOTFS_IMG_URL} -o ${WEBSRV_FOLDER}/images/${ROOTFS_IMG_URL##*/}
curl -Lk ${LIVE_ISO_URL} -o ${WEBSRV_FOLDER}/images/${LIVE_ISO_URL##*/}
chmod -R 755 ${WEBSRV_FOLDER}/*

## Run Webserver
podman ps --noheading | grep -q websrv-ai
if [[ $? == 0 ]];then
    echo "Launching Registry pod..."
    /usr/bin/podman run --name websrv-ai --net host -v /opt/srv:/usr/local/apache2/htdocs:z quay.io/alosadag/httpd:p8080
fi
```

The script will create a folder under `/opt/srv`. This folder will contain the `images` for RHCOS provision in the worker nodes. To be more concrete, we need the `RootFS` and `LiveISO` artifacts found on the Openshift CI Release page.

After the download, a container will run to host the images under a webserver. It uses a variation of the official httpd image, which also allows it to work with IPv6.
