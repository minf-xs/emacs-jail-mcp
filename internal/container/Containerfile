FROM debian:trixie-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        emacs \
        xvfb \
        imagemagick \
        git \
        ripgrep \
        ca-certificates \
        procps \
        bash \
    && rm -rf /var/lib/apt/lists/*
