
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
ADMIN_TOKEN=admin4ILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z
...


### Flutter
Generate app icon
flutter pub run flutter_launcher_icons

### Install
```shell
sudo su
mkdir -p /usr/local/bin/procsentinel
tar -xzvf procsentinel-server.tar.gz -C /usr/local/bin/procsentinel
cp /usr/local/bin/procsentinel/procsentinel.service /etc/systemd/system/procsentinel.service
sudo systemctl daemon-reload
sudo systemctl enable procsentinel
sudo systemctl start procsentinel
sudo systemctl status procsentinel
```