# Build from repository root: docker build -f test/fixtures/runtime/Dockerfile -t bitrix-runtime-fixture .
FROM quay.io/bitrix24/php:8.5.12-fpm-v1-alpine@sha256:e0f4ef993f5816a446ada5fbc5145b2eb3ef2c79e5590be2a02c1928fce97c4d
USER root
COPY test/fixtures/runtime/ /opt/www/
COPY internal/runtime/templates/bitrix/ /tmp/operator-templates/
COPY hack/runtime/prepare-release.sh /tmp/prepare-release.sh
RUN sh /tmp/prepare-release.sh /opt/www /tmp/operator-templates \
    && rm -rf /tmp/operator-templates /tmp/prepare-release.sh /opt/www/Dockerfile /opt/www/Dockerfile.dockerignore \
    && chmod -R a+rX /opt/www
LABEL team.m8.bitrix-operator.runtime-contract="v1"
USER 10001:10001
WORKDIR /opt/www
