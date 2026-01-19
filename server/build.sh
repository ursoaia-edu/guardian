go build -o procsentinel-server


TAR_FILE="procsentinel-server.tar.gz"

echo "Creating archive: $TAR_FILE"

tar -czf "$TAR_FILE" \
  .env \
  procsentinel-server \
  procsentinel.service

mkdir -p ../release
cp install.sh ../release/
mv "$TAR_FILE" ../release/
# rm procsentinel-server
