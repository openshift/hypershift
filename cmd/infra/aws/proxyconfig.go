package aws

import "fmt"

const proxyConfigurationScript = `#!/bin/bash
yum install -y squid
# By default, squid only allows connect on port 443
sed -E 's/(^http_access deny CONNECT.*)/#\1/' -i /etc/squid/squid.conf
systemctl enable --now squid

mkdir -p /home/ec2-user/.ssh
chmod 0700 /home/ec2-user/.ssh
echo -e '%s' >/home/ec2-user/.ssh/authorized_keys
chmod 0600 /home/ec2-user/.ssh/authorized_keys
chown -R ec2-user:ec2-user /home/ec2-user/.ssh
`

const secureProxyConfigurationScript = `#!/bin/bash
curl -OL https://snapshots.mitmproxy.org/7.0.2/mitmproxy-7.0.2-linux.tar.gz
tar xzvf mitmproxy-7.0.2-linux.tar.gz -C /usr/bin
cat <<EOF > /usr/bin/run-mitm
#!/bin/bash
mitmdump --showhost --ssl-insecure \
  -p 3128 \
  --ignore-hosts '.*'
EOF

chmod +x /usr/bin/run-mitm

cat <<EOF > /lib/systemd/system/mitmproxy.service
[Unit]
Description=mitmdump service
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/bin/run-mitm
Restart=always
RestartSec=1

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable mitmproxy
systemctl start mitmproxy

mkdir -p /home/ec2-user/.ssh
chmod 0700 /home/ec2-user/.ssh
echo -e '%s' >/home/ec2-user/.ssh/authorized_keys
chmod 0600 /home/ec2-user/.ssh/authorized_keys
chown -R ec2-user:ec2-user /home/ec2-user/.ssh
`

func proxyConfigScript(isSecure bool, publicSSHKey string) string {
	if isSecure {
		return fmt.Sprintf(secureProxyConfigurationScript, publicSSHKey)
	}
	return fmt.Sprintf(proxyConfigurationScript, publicSSHKey)
}
