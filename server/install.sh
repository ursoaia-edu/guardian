sudo mkdir -p /usr/local/bin/procsentinel
sudo tar -xzvf procsentinel-server.tar.gz -C /usr/local/bin/procsentinel
sudo cp /usr/local/bin/procsentinel/procsentinel.service /etc/systemd/system/procsentinel.service
sudo systemctl daemon-reload
sudo systemctl enable procsentinel
sudo systemctl start procsentinel
sudo systemctl status procsentinel