FROM alpine:3.20

RUN apk add --no-cache \
    bash \
    curl \
    jq \
    ca-certificates \
    coreutils \
    gettext \
    kubectl \
    docker-cli \
    iproute2 \
  && update-ca-certificates

WORKDIR /runner
COPY . .
ENTRYPOINT ["/runner/run.sh"]
