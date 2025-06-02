FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.3-bookworm@sha256:5d129268e7be6593afce913567b96d560bccf95461c30fabf82e3caa63c4b45d

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
