FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.1-bookworm@sha256:2fb5841e5d456d450a7b2f460b15e65c918ffb381fd54f4f147e6c0c14204083

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
