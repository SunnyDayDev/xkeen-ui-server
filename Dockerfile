FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm@sha256:8d48e12ec56735e9358640898b9d9b9fcca110612ed8a5567438c0a1baa24e66 AS build
WORKDIR /src
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN test "$(go env GOVERSION)" = "$(awk '$1 == "toolchain" { print $2 }' go.mod)" \
    && go mod download
COPY cmd/xkeen-ui-server ./cmd/xkeen-ui-server
COPY internal ./internal
ARG TARGETOS
ARG TARGETARCH
RUN install -d -m 0700 /rootfs/var/lib/xkeen-ui-server /rootfs/var/lib/xkeen-ui-server/state \
    && CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath \
       -o /xkeen-ui-server ./cmd/xkeen-ui-server

FROM scratch
COPY --from=build --chown=65532:65532 /rootfs/ /
COPY --from=build /xkeen-ui-server /usr/local/bin/xkeen-ui-server
WORKDIR /var/lib/xkeen-ui-server
ENV TMPDIR=/var/lib/xkeen-ui-server/state
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/xkeen-ui-server"]
CMD ["--listen", "0.0.0.0:8080"]
