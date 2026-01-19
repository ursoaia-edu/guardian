FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /procsentinel-server .

FROM alpine:3.21
COPY --from=build /procsentinel-server /usr/local/bin/
WORKDIR /data
EXPOSE 8080
CMD ["procsentinel-server"]
