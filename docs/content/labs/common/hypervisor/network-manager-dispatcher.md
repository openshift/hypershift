This script modifies the system DNS resolver to prioritize pointing to the `dnsmasq` service (configured later). This ensures that virtual machines can resolve the various domains, routes, and registries required for the different steps of the process.

To enable this, you need to create a script named `forcedns` in `/etc/NetworkManager/dispatcher.d/` with the following content:

!!! note

    Please ensure you modify the appropriate fields to align with your laboratory environment.

=== "IPv4"

    ```bash
    #!/bin/bash

    export IP="192.168.125.1"
    export BASE_RESOLV_CONF="/run/NetworkManager/resolv.conf"

    if ! [[ `grep -q "$IP" /etc/resolv.conf` ]]; then
    export TMP_FILE=$(mktemp /etc/forcedns_resolv.conf.XXXXXX)
    cp $BASE_RESOLV_CONF $TMP_FILE
    chmod --reference=$BASE_RESOLV_CONF $TMP_FILE
    sed -i -e "s/hypershiftbm.lab//" -e "s/search /& hypershiftbm.lab /" -e "0,/nameserver/s/nameserver/& $IP\n&/" $TMP_FILE
    mv $TMP_FILE /etc/resolv.conf
    fi
    echo "ok"
    ```

=== "IPv6"

    ```bash
    #!/bin/bash

    export IP="2620:52:0:1306::1"
    export BASE_RESOLV_CONF="/run/NetworkManager/resolv.conf"

    if ! [[ `grep -q "$IP" /etc/resolv.conf` ]]; then
    export TMP_FILE=$(mktemp /etc/forcedns_resolv.conf.XXXXXX)
    cp $BASE_RESOLV_CONF $TMP_FILE
    chmod --reference=$BASE_RESOLV_CONF $TMP_FILE
    sed -i -e "s/hypershiftbm.lab//" -e "s/search /& hypershiftbm.lab /" -e "0,/nameserver/s/nameserver/& $IP\n&/" $TMP_FILE
    mv $TMP_FILE /etc/resolv.conf
    fi
    echo "ok"
    ```

=== "Dual stack"

    ```bash
    #!/bin/bash

    export IP="192.168.126.1"
    export BASE_RESOLV_CONF="/run/NetworkManager/resolv.conf"

    if ! [[ `grep -q "$IP" /etc/resolv.conf` ]]; then
    export TMP_FILE=$(mktemp /etc/forcedns_resolv.conf.XXXXXX)
    cp $BASE_RESOLV_CONF $TMP_FILE
    chmod --reference=$BASE_RESOLV_CONF $TMP_FILE
    sed -i -e "s/hypershiftbm.lab//" -e "s/search /& hypershiftbm.lab /" -e "0,/nameserver/s/nameserver/& $IP\n&/" $TMP_FILE
    mv $TMP_FILE /etc/resolv.conf
    fi
    echo "ok"
    ```

The `IP` variable at the beginning of the script must be modified to point to the IP address of the Hypervisor's interface hosting the Openshift management cluster.

After creating the file, you need to add execution permissions using the command:

```bash
chmod 755 /etc/NetworkManager/dispatcher.d/forcedns
```

Then, execute it once. The output should indicate `ok`.

