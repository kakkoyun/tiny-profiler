FROM --platform="${BUILDPLATFORM:-linux/amd64}" docker.io/library/busybox:1.35.0 as builder
RUN mkdir /.cache && touch -t 202101010000.00 /.cache

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT

WORKDIR /app
COPY goreleaser/dist dist

# NOTICE: See goreleaser.yml for the build paths.
RUN if [ "${TARGETARCH}" == 'amd64' ]; then \
        cp "dist/tiny-profiler-${TARGETARCH}_${TARGETOS}_${TARGETARCH}_${TARGETVARIANT:-v1}/tiny-profiler" . ; \
    elif [ "${TARGETARCH}" == 'arm' ]; then \
        cp "dist/tiny-profiler-${TARGETARCH}_${TARGETOS}_${TARGETARCH}_${TARGETVARIANT##v}/tiny-profiler" . ; \
    else \
        cp "dist/tiny-profiler-${TARGETARCH}_${TARGETOS}_${TARGETARCH}/tiny-profiler" . ; \
    fi
RUN chmod +x tiny-profiler

FROM --platform="${TARGETPLATFORM:-linux/amd64}" gcr.io/distroless/static@sha256:21d3f84a4f37c36199fd07ad5544dcafecc17776e3f3628baf9a57c8c0181b3f
COPY --chown=0:0 --from=builder /app/tiny-profiler /bin/tiny-profiler
CMD ["/bin/tiny-profiler"]
