FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26-bookworm@sha256:4e652d1254a73a25bbc34dfda45dfca5dfae927f7245cc914310ff1057580499

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
