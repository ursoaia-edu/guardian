sudo su
mkdir -p /usr/local/bin/procsentinel
tar -xzvf procsentinel-server.tar.gz -C /usr/local/bin/procsentinel
cp /usr/local/bin/procsentinel/procsentinel.service /etc/systemd/system/procsentinel.service
systemctl daemon-reload
systemctl enable procsentinel
systemctl start procsentinel
systemctl status procsentinel