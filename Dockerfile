# GoReleaser builds the binaries and this image only copies them in, so the
# image is the harness, its CA certificates and nothing else.
#
# The Rust runner is not in here. It needs a Rust toolchain to build and the
# point of this image is a small thing to drop on a machine that is about to be
# measured, so a run from the image compares the Go engines and a run that
# includes Tantivy is built from source with `make build`.
FROM gcr.io/distroless/static:nonroot

COPY kura-bench /usr/bin/kura-bench
COPY kura-corpus /usr/bin/kura-corpus
COPY bleve-runner /usr/bin/bleve-runner
COPY sqlitefts-runner /usr/bin/sqlitefts-runner
COPY genba-runner /usr/bin/genba-runner

USER nonroot:nonroot
WORKDIR /bench

ENTRYPOINT ["/usr/bin/kura-bench"]
