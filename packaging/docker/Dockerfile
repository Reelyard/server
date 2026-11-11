# Reelyard container image, built by GoReleaser (dockers_v2): the binary for
# each platform is placed at $TARGETPLATFORM/reelyard in the build context.
FROM alpine:3.22

# ffmpeg/ffprobe: language checks on import, thumbnails and transcoding
# for DLNA. tzdata: local times in logs.
# su-exec: drop from root to PUID:PGID in the entrypoint.
RUN apk add --no-cache ca-certificates tzdata ffmpeg su-exec \
 && mkdir -p /config /data/downloads /data/library \
 && chown -R 1000:1000 /config /data

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/reelyard /usr/bin/reelyard
COPY packaging/docker/entrypoint.sh /usr/local/bin/reelyard-entrypoint

# One /data volume holds downloads and library, so imports are hardlinks
# (no second copy). Mount separate volumes and Reelyard copies instead.
ENV REELYARD_DATA_DIR=/config \
    REELYARD_DOWNLOAD_DIR=/data/downloads \
    REELYARD_LIBRARY_DIR=/data/library \
    REELYARD_LOG_FILE=false \
    PUID=1000 \
    PGID=1000 \
    UMASK=002

VOLUME ["/config", "/data"]
# Web UI, torrents (TCP+UDP), DLNA. DLNA discovery needs --network host.
EXPOSE 8787 42069 42069/udp 8200

# Starts as root only to set folder owners, then runs as PUID:PGID.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
  CMD wget -q -O /dev/null http://127.0.0.1:8787/api/v1/health || exit 1

ENTRYPOINT ["/usr/local/bin/reelyard-entrypoint"]
CMD ["serve"]
