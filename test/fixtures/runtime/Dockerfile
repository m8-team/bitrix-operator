# Build from repository root: docker build -f test/fixtures/runtime/Dockerfile -t bitrix-runtime-fixture .
FROM quay.io/bitrix24/php:8.2.34-fpm-v1-alpine@sha256:58aa3acaaf6da1f38c2297abade120780be02afad99656f97c444fa3207ab440
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
