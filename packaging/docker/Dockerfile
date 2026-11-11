# Reelyard container image, built by GoReleaser (dockers_v2): the binary for
# each platform is placed at $TARGETPLATFORM/reelyard in the build context.
FROM alpine:3.22

# ffmpeg/ffprobe: language checks on import, thumbnails and transcoding
# for DLNA. tzdata: local times in logs.
RUN apk add --no-cache ca-certificates tzdata ffmpeg \
 && addgroup -S -g 1000 reelyard \
 && adduser -S -D -H -u 1000 -G reelyard -h /config reelyard \
 && mkdir -p /config /data/downloads /data/library \
 && chown -R reelyard:reelyard /config /data

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/reelyard /usr/bin/reelyard

# One /data volume holds downloads and library, so imports are hardlinks
# (no second copy). Mount separate volumes and Reelyard copies instead.
ENV REELYARD_DATA_DIR=/config \
    REELYARD_DOWNLOAD_DIR=/data/downloads \
    REELYARD_LIBRARY_DIR=/data/library \
    REELYARD_LOG_FILE=false

VOLUME ["/config", "/data"]
# Web UI, torrents (TCP+UDP), DLNA. DLNA discovery needs --network host.
EXPOSE 8787 42069 42069/udp 8200

USER reelyard
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
  CMD wget -q -O /dev/null http://127.0.0.1:8787/api/v1/health || exit 1

ENTRYPOINT ["/usr/bin/reelyard"]
CMD ["serve"]
