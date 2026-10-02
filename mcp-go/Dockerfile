# Build from the repository root, so the guide and preview come from mcp/:
#   docker build -f mcp-go/Dockerfile -t sliqtly-mcp-go .
FROM golang:1.26 AS build
WORKDIR /src/mcp-go
COPY mcp-go/go.mod mcp-go/go.sum ./
RUN go mod download
COPY mcp-go/ ./
COPY mcp/guide.md mcp/src/preview.html ./assets/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server .

# no shell, no libc: the binary, CA certificates and a nonroot user
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /server
ENTRYPOINT ["/server"]
