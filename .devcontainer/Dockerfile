FROM mcr.microsoft.com/oss/go/microsoft/golang:1.27-bookworm@sha256:93e27cb4199b32131586232fc4c490559b71784d62824ec09abf483438994f4f

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
