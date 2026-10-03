# Build from the repository root:
#   docker build -f mcp-go/Dockerfile -t sliqtly-mcp-go .
# The first stage compiles rgr/ and the editor's deck model (src/) to Go with
# Ranger, cloned at the ref in presentation.config.json.
FROM node:22 AS gen
WORKDIR /src
COPY presentation.config.json ./
COPY scripts/lib.mjs scripts/
COPY src/ src/
COPY mcp-go/gen.mjs mcp-go/
COPY mcp-go/rgr/ mcp-go/rgr/
RUN node mcp-go/gen.mjs

FROM golang:1.26 AS build
WORKDIR /src/mcp-go
COPY mcp-go/go.mod mcp-go/go.sum ./
RUN go mod download
COPY mcp-go/ ./
COPY --from=gen /src/mcp-go/sliqtly_mcp.go ./
COPY --from=gen /src/mcp-go/presdata/presdata.go ./presdata/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server .

# no shell, no libc: the binary, CA certificates and a nonroot user
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /server
ENTRYPOINT ["/server"]
