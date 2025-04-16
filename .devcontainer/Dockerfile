FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.2-bookworm@sha256:acc7968e8119c54a4dab06cb09d4d043dd18d0878d271d569fad350850d3f041

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
