# See here for image contents: https://github.com/devcontainers/images/tree/main/src/go

# [Choice] Go version (use -bullseye variants on local arm64/Apple Silicon): 1, 1.16, 1.17, 1-bullseye, 1.16-bullseye, 1.17-bullseye, 1-buster, 1.16-buster, 1.17-buster
ARG VARIANT="1.18-bullseye"
FROM mcr.microsoft.com/vscode/devcontainers/go:1-${VARIANT}

# [Optional] Uncomment this section to install additional OS packages.
# graphviz for pprof
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends graphviz

RUN curl -Lo skaffold https://storage.googleapis.com/skaffold/releases/v2.11.1/skaffold-linux-amd64 && \
    install skaffold /usr/local/bin/

# [Optional] Uncomment the next lines to use go get to install anything else you need
USER vscode
RUN go install github.com/google/pprof@latest

# [Optional] Uncomment this line to install global node packages.
# RUN su vscode -c "source /usr/local/share/nvm/nvm.sh && npm install -g <your-package-here>" 2>&1
