# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates && \
    addgroup -g 10001 app && adduser -D -u 10001 -G app app && \
    mkdir -p /data && chown app:app /data
COPY --from=build /out/server /usr/local/bin/server
USER 10001:10001
ENV LISTEN_ADDR=0.0.0.0:8080 ALLOW_NON_LOOPBACK=1 STATE_PATH=/data/state.json
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/server"]
