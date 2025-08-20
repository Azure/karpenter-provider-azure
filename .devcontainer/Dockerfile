FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.6-bookworm@sha256:4bf921879edc69ca12c81de48583f4de79acab97e56d2f9371c31da6ac6b44d9

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
