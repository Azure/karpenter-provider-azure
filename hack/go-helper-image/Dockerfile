ARG GOVERSION=1.21
FROM --platform=$BUILDPLATFORM golang:${GOVERSION} as delve
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

ARG DELVE_VERSION=1.21.0

# Patch delve to make defaults for --check-go-version and --only-same-user
# to be set at build time.  We must install patch(1) to apply the patch.
#
# We default --check-go-version to false to support binaries compiled
# with unsupported versions of Go.  Delve issues a prominent warning.
# 
# We default --only-same-user to false as `kubectl port-forward`
# to dlv port is refused otherwise.
RUN apt-get update && apt-get install -y --no-install-recommends \
  patch
RUN curl --location --output delve.tar.gz https://github.com/go-delve/delve/archive/v$DELVE_VERSION.tar.gz \
  && tar xzf delve.tar.gz \
  && mv delve-$DELVE_VERSION delve-source
COPY delve-*.patch .
RUN patch -p0 -d delve-source < delve-as-options.patch

# Produce an as-static-as-possible dlv binary to work on musl and glibc
RUN cd delve-source \
  && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /go/dlv -ldflags '-s -w -X github.com/go-delve/delve/cmd/dlv/cmds.checkGoVersionDefault=false -X github.com/go-delve/delve/cmd/dlv/cmds.checkLocalConnUserDefault=false -extldflags "-static"' ./cmd/dlv/

# Now populate the duct-tape image with the language runtime debugging support files
# The debian image is about 95MB bigger
FROM busybox
# The install script copies all files in /duct-tape to /dbg
COPY install.sh /
CMD ["/bin/sh", "/install.sh"]
WORKDIR /duct-tape
COPY --from=delve /go/dlv go/bin/
