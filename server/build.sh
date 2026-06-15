# Deploy order: run "./guardian-server migrate" once with MIGRATE_DATABASE_URL
# set, then start the service. The service itself cannot alter the schema.
go build -o guardian-server

cp guardian-server ../dist/server/


# TAR_FILE="guardian-server.tar.gz"

# echo "Creating archive: $TAR_FILE"

# tar -czf "$TAR_FILE" \
#   .env \
#   guardian-server \
#   guardian.service

# mkdir -p ../dist
# cp install.sh ../dist/
# mv "$TAR_FILE" ../dist/
rm guardian-server
