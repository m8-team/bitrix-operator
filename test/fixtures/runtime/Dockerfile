# Build from repository root: docker build -f test/fixtures/runtime/Dockerfile -t bitrix-runtime-fixture .
FROM quay.io/bitrix24/php:8.5.11-fpm-v1-alpine@sha256:ba888be2f4d35217bf1027d5fa11541b36a24c208c9753324397ef0a446ba2a8
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
