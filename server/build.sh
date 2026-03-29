go build -o guardian-server

mv guardian-server ../dist/


# TAR_FILE="guardian-server.tar.gz"

# echo "Creating archive: $TAR_FILE"

# tar -czf "$TAR_FILE" \
#   .env \
#   procsentinel-server \
#   procsentinel.service

# mkdir -p ../dist
# cp install.sh ../dist/
# mv "$TAR_FILE" ../dist/
# rm procsentinel-server
