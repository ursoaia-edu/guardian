# Packages an already-built guardian-server binary; it does not compile Go.
# server/build.sh produces that binary as a static Linux executable
# (CGO_ENABLED=0), which is what lets it run on Alpine's musl libc at all —
# a binary built with plain "go build" on a glibc host dies here with a
# misleading "not found" the moment the container starts.
#
# ca-certificates is not needed by anything the server does today, but the
# first outbound HTTPS call (Stripe, email) would fail without it, and that
# failure would look like a networking problem rather than a missing package.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -S -D -H -u 10001 guardian
COPY guardian-server /usr/local/bin/guardian-server
WORKDIR /usr/local/bin
USER guardian
EXPOSE 8080
CMD ["guardian-server"]
