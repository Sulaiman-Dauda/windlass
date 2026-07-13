# Windlass container image. The panel needs the docker CLI (+ compose
# plugin) and git; the host's Docker socket is mounted at runtime.
#
#   docker build -t windlass .
#   see install/docker-compose.install.yaml for running it

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

FROM alpine:3.21
RUN apk add --no-cache docker-cli docker-cli-compose git ca-certificates
COPY --from=build /windlass /usr/local/bin/windlass
ENV WINDLASS_DATA=/var/lib/windlass \
    WINDLASS_NO_SELF_UPDATE=1
VOLUME /var/lib/windlass
EXPOSE 8080
ENTRYPOINT ["windlass"]
