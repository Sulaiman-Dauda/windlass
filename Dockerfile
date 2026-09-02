# Windlass container image. The panel needs the docker CLI (+ compose
# plugin) and git; the host's Docker socket is mounted at runtime.
#
#   docker build -t windlass .                     builds from source
#   docker build -t windlass --build-arg BINARY_STAGE=prebuilt .
#                                                  uses dist/windlass-linux-$TARGETARCH
#   see install/docker-compose.install.yaml for running it
#
# The release workflows cross-compile both architectures natively and then
# select the prebuilt stage, so nothing but a small apk install runs under
# emulation. Compiling Go and npm under QEMU for arm64 ran past the six hour
# job ceiling and was cancelled every time, which is why no image was ever
# published.

# Declared before the first FROM so it can be used in one.
ARG BINARY_STAGE=build

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -tags embedweb \
    -ldflags "-s -w -X github.com/windlass-dev/windlass/internal/version.Version=${VERSION}" \
    -o /windlass ./cmd/windlass

# Prebuilt: the caller cross-compiled into dist/ already. TARGETARCH is set by
# buildx once per platform in the manifest list, and matches the file names the
# release workflows write.
FROM alpine:3.21 AS prebuilt
ARG TARGETARCH
COPY --chmod=0755 dist/windlass-linux-${TARGETARCH} /windlass

# Whichever of the two stages above supplied the binary.
FROM ${BINARY_STAGE} AS binary

FROM alpine:3.21
RUN apk add --no-cache docker-cli docker-cli-compose git ca-certificates
COPY --from=binary /windlass /usr/local/bin/windlass
ENV WINDLASS_DATA=/var/lib/windlass \
    WINDLASS_NO_SELF_UPDATE=1
VOLUME /var/lib/windlass
EXPOSE 8080
ENTRYPOINT ["windlass"]
