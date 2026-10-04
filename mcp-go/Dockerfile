# syntax=docker/dockerfile:1
# Build from the repository root:
#   docker build -f mcp-go/Dockerfile -t sliqtly-mcp-go .                  # Cloud Run
#   docker build -f mcp-go/Dockerfile --target local -t sliqtly-server .   # with the editor
# The first stage compiles rgr/ and the editor's deck model (src/) to Go with
# Ranger, cloned at the ref in presentation.config.json, with RangerFlow,
# RangerMarkdown and RangerPPTX, which are private: give a token that can read them as a
# build secret, docker build --secret id=deps_token,env=DEPS_TOKEN …
FROM node:22 AS gen
WORKDIR /src
COPY presentation.config.json ./
COPY scripts/lib.mjs scripts/
COPY src/ src/
COPY themes/ themes/
COPY mcp-go/gen.mjs mcp-go/
COPY mcp-go/rgr/ mcp-go/rgr/
COPY scripts/ci-git-auth.sh scripts/
RUN --mount=type=secret,id=deps_token \
    if [ -s /run/secrets/deps_token ]; then DEPS_TOKEN=$(cat /run/secrets/deps_token) sh scripts/ci-git-auth.sh; fi && \
    node mcp-go/gen.mjs

FROM golang:1.26 AS build
WORKDIR /src/mcp-go
COPY mcp-go/go.mod mcp-go/go.sum ./
RUN go mod download
COPY mcp-go/ ./
COPY --from=gen /src/mcp-go/sliqtly_mcp.go ./
COPY --from=gen /src/mcp-go/presdata/presdata.go ./presdata/
COPY --from=gen /src/mcp-go/fonts/ ./fonts/
COPY --from=gen /src/mcp-go/themes/ ./themes/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server . && mkdir /data

# A server of one's own with the editor and player built in (localweb.go):
# web/dist is built here and copied in before the binary is built again.
FROM node:22 AS web
WORKDIR /src
COPY . .
RUN --mount=type=secret,id=deps_token \
    if [ -s /run/secrets/deps_token ]; then DEPS_TOKEN=$(cat /run/secrets/deps_token) sh scripts/ci-git-auth.sh; fi && \
    npm ci --no-audit --no-fund && NODE_OPTIONS=--max-old-space-size=6144 node scripts/build.mjs

FROM build AS build-local
COPY --from=web /src/web/dist/ ./webdist/
RUN rm -rf webdist/fonts && CGO_ENABLED=0 go build -tags nocloud -trimpath -ldflags="-s -w" -o /server .

FROM gcr.io/distroless/static-debian12:nonroot AS local
COPY --from=build-local /server /server
COPY --from=build --chown=65532:65532 /data /data
ENV SLIQTLY_DATA=/data
# in a container every interface is the container's own: the port mapping
# (docker run -p) decides who reaches it
ENV SLIQTLY_LISTEN=network
EXPOSE 8080
ENTRYPOINT ["/server"]

# Cloud Run (the default target): no shell, no libc, the binary, CA
# certificates and a nonroot user
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /server
# the folder a server of one's own keeps decks in (SLIQTLY_DATA=/data)
COPY --from=build --chown=65532:65532 /data /data
ENV SLIQTLY_LISTEN=network
ENTRYPOINT ["/server"]
