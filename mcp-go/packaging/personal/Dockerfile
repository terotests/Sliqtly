# The Personal image (ghcr.io/terotests/sliqtly-personal): the server with
# the public viewer built in and no editor, as in the Personal .deb. Built by
# .github/workflows/personal-package.yml from binaries it compiled already:
#   dist/bin/linux_<arch>/sliqtly-server   (SLIQTLY_BUILD_WEB=viewer)
#   dist/empty/                            an empty folder, for /data
# with the context mcp-go/:
#   docker buildx build -f packaging/personal/Dockerfile --platform linux/amd64,linux/arm64 .
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETARCH
COPY dist/bin/linux_${TARGETARCH}/sliqtly-server /sliqtly-server
COPY packaging/personal/LICENSE /LICENSE
COPY --chown=65532:65532 dist/empty/ /data/
COPY --chown=65532:65532 dist/empty/ /backup/
ENV SLIQTLY_DATA=/data \
    SLIQTLY_BACKUP=/backup \
    PORT=8080 \
    SLIQTLY_LISTEN=network
# every interface inside the container is the container's own: who reaches
# the server is decided by docker run -p (127.0.0.1:8080:8080 = this
# computer only)
EXPOSE 8080
VOLUME ["/data", "/backup"]
LABEL org.opencontainers.image.title="Sliqtly Personal" \
      org.opencontainers.image.description="Sliqtly MCP server and presentation viewer. Free for personal use and qualifying solo businesses; see /LICENSE." \
      org.opencontainers.image.url="https://sliqtly.com/local.html" \
      org.opencontainers.image.source="https://github.com/terotests/Sliqtly" \
      org.opencontainers.image.licenses="LicenseRef-Sliqtly-Personal"
ENTRYPOINT ["/sliqtly-server"]
