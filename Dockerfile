# The API image. Two stages: compile with the Go toolchain, ship a binary on its own.
#
# The Go version is NOT written here: it is read from go.mod through the build image's tag.
# Duplicating a version number guarantees that one of the two ends up forgotten.

FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first, in their own layer: as long as go.mod and go.sum do not move, the download is
# reused from one image to the next.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO disabled: the binary has to run as it is in an image with no libc.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/flowlio ./cmd/flowlio

FROM alpine:3.21

# Root certificates: the API talks to Neon over TLS in production.
RUN apk add --no-cache ca-certificates

# Unprivileged user. The configuration directory belongs to it, otherwise writing the credentials
# file on the first start would fail.
RUN adduser -D -u 10001 flowlio && mkdir -p /home/flowlio/.config && chown -R flowlio /home/flowlio
USER flowlio

COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/flowlio /usr/local/bin/flowlio

EXPOSE 42058

ENTRYPOINT ["/usr/local/bin/api"]
