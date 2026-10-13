In a bare metal environment, the preferred approach is to utilize the actual BMC (Baseboard Management Controller) of the nodes used for the management cluster, which can be managed by Metal3 for discovery and provisioning. However, in a virtual environment, this approach is not feasible. As a workaround, we will use `ksushy`, which is an implementation of `sushy-tools`, allowing us to simulate BMCs for the virtual machines.

To configure `ksushy`, execute the following commands:


```bash
sudo dnf install python3-pyOpenSSL.noarch python3-cherrypy -y
kcli create sushy-service --ssl --ipv6 --port 9000
sudo systemctl daemon-reload
systemctl enable --now ksushy
```

To test if this service is functioning correctly, you can check the service status with `systemctl status ksushy`. Additionally, you can execute a `curl` command against the exposed interface:

```
curl -Lk https://[2620:52:0:1306::1]:9000/redfish/v1
```
