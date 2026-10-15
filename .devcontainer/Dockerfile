FROM mcr.microsoft.com/oss/go/microsoft/golang:1.27-bookworm@sha256:7ff46ce2498a27b9f00537a945d5d6cbad7e11f695e66ba5c922038387944f40

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
