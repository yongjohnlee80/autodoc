# autodoc --server in a container: docs/enterprise/operations.md.
#
#   docker build -f deploy/container/Dockerfile -t autodoc-server .
#
# The binary is static (CGO_ENABLED=0), so the image is distroless: no shell, no package manager,
# no curl. Its healthcheck is autodoc's own probe (--server --health), which reads [http] from the
# same server.toml.

FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /autodoc ./cmd/autodoc

FROM gcr.io/distroless/static:nonroot
COPY --from=build /autodoc /autodoc
USER nonroot:nonroot
EXPOSE 8730
ENTRYPOINT ["/autodoc", "--server", "/etc/autodoc/server.toml"]
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
  CMD ["/autodoc", "--server", "--health", "/etc/autodoc/server.toml"]
