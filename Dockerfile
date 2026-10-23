# syntax=docker/dockerfile:1.7

# Multi-architecture: the frontend and Go builds run on the build machine's own platform
# ($BUILDPLATFORM) and Go cross-compiles for the target, so arm64 images build without emulation:
#   docker buildx build --platform linux/amd64,linux/arm64 -t dbyson .

# ---- Frontend (platform-independent output) ----
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# The frontend imports the shared project logo from the repository root.
COPY logo-dbyson.png /logo-dbyson.png
RUN npm run build

# ---- Backend ----
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/dbyson ./cmd/dbyson
RUN go build -trimpath -ldflags "-s -w" -o /out/docker-discovery ./cmd/docker-discovery
# Empty data dir owned by the distroless "nonroot" user (uid 65532); mounted as a volume at runtime.
RUN mkdir /out/data && chmod 700 /out/data

# ---- Runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/dbyson /dbyson
COPY --from=build /out/docker-discovery /docker-discovery
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
VOLUME /data
ENV DBYSON_LISTEN=0.0.0.0:8080 \
    DBYSON_DATA_DIR=/data
EXPOSE 8080
ENTRYPOINT ["/dbyson"]
