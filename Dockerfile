# htmlprint image: a current Chromium plus the printkit CLI.
#
# The image runs as a non-root user. Many container runtimes still cannot
# start the Chromium sandbox (user namespaces are disabled), so the image sets
# PRINTKIT_NO_SANDBOX=1. Unset it when the runtime allows the sandbox.
#
# /dev/shm is often small. The library already passes --disable-dev-shm-usage.
# If you drop that, mount a larger /dev/shm (for example `shm_size: 256mb`).

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/htmlprint ./cmd/htmlprint

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        chromium \
        ca-certificates \
        fonts-liberation \
        fonts-noto-core \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 printkit
COPY --from=build /out/htmlprint /usr/local/bin/htmlprint
USER printkit
ENV CHROME_PATH=/usr/bin/chromium \
    PRINTKIT_NO_SANDBOX=1
WORKDIR /home/printkit
ENTRYPOINT ["htmlprint"]
CMD ["--help"]
