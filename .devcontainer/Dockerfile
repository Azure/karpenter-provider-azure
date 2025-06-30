FROM mcr.microsoft.com/oss/go/microsoft/golang:1.24.4-bookworm@sha256:5f38e33738c01f10d16fd41eed6b5bb2f675a4f7820e473d9d92d1fbd8be200d

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz
