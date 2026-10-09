# Static cometseed binary in a distroless image, running as an unprivileged user.
#
#   docker build -t cometseed --build-arg VERSION=$(git describe --tags --always) .
#   docker run -d --name cometseed -p 26666:26666 \
#     -v $PWD/config.toml:/config/config.toml:ro -v cometseed-data:/data cometseed

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.buildVersion=${VERSION}" -o /out/cometseed . \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cometseed /usr/local/bin/cometseed
# owned by the nonroot user, so a new named volume on /data is writable
COPY --from=build --chown=65532:65532 /out/data /data
ENV COMETSEED_CONFIG=/config/config.toml
VOLUME ["/data"]
USER nonroot
ENTRYPOINT ["/usr/local/bin/cometseed"]
CMD ["start"]
