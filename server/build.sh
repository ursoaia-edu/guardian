go build -o ../bin/procsentinel-server
cp .env ../bin/
cp procsentinel.service ../bin/

tar -czvf ../procsentinel-server.tar.gz -C ../bin .
mkdir -p ../release
cp install.sh ../release/
mv ../procsentinel-server.tar.gz ../release/
