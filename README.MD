
# Agent
```.env
SERVER_ADDRESS=http://35.189.195.212:8080
TOKEN=mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z1
```

# Server
```.env
SERVER_ADDRESS=http://0.0.0.0:8080
WEB_DIR=web
TOKEN=mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z1
...


### Flutter
Generate app icon
flutter pub run flutter_launcher_icons

### Deploy
```shell
sudo su
mkdir -p /usr/local/bin/procsentinel
tar -xzvf procsentinel-server.tar.gz -C /usr/local/bin/procsentinel
cp /usr/local/bin/procsentinel/procsentinel.service /etc/systemd/system/procsentinel.service
systemctl daemon-reload
systemctl enable procsentinel
systemctl start procsentinel
systemctl status procsentinel
```