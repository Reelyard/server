# Harbor container image, built by GoReleaser (dockers_v2): the binary for
# each platform is placed at $TARGETPLATFORM/harbor in the build context.
FROM alpine:3.22

# ffmpeg/ffprobe: language checks on import, thumbnails and transcoding
# for DLNA. tzdata: local times in logs.
RUN apk add --no-cache ca-certificates tzdata ffmpeg \
 && addgroup -S -g 1000 harbor \
 && adduser -S -D -H -u 1000 -G harbor -h /config harbor \
 && mkdir -p /config /data/downloads /data/library \
 && chown -R harbor:harbor /config /data

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/harbor /usr/bin/harbor

# One /data volume holds downloads and library, so imports are hardlinks
# (no second copy). Mount separate volumes and Harbor copies instead.
ENV HARBOR_DATA_DIR=/config \
    HARBOR_DOWNLOAD_DIR=/data/downloads \
    HARBOR_LIBRARY_DIR=/data/library \
    HARBOR_LOG_FILE=false

VOLUME ["/config", "/data"]
# Web UI, torrents (TCP+UDP), DLNA. DLNA discovery needs --network host.
EXPOSE 8787 42069 42069/udp 8200

USER harbor
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
  CMD wget -q -O /dev/null http://127.0.0.1:8787/api/v1/health || exit 1

ENTRYPOINT ["/usr/bin/harbor"]
CMD ["serve"]
