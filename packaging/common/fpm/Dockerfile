FROM node:24-bookworm

VOLUME /repo
WORKDIR /repo

ENV PACKAGE="deb"
ENV VERSION=""
ENV ARCH="amd64"
ENV OUTPUT_DIR="/repo/build/packages/"

COPY install-deps.sh Gemfile /

RUN /install-deps.sh

RUN git config --global --add safe.directory /repo
