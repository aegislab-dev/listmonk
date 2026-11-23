# 시험용 공통 베이스 이미지 (LST-51). 의존성 파일(package.json·yarn.lock 4개)·이 파일·stuffbin 버전이 같으면 한 번만 만든다.
# Dockerfile.axm 의 빌더 단계와 같은 줄: Go 1.26.11 + Node 24.22.2 + Yarn 1.22.22.
ARG GO_IMAGE=golang:1.26.11-bookworm
FROM node:24.22.2-bookworm-slim AS node

FROM ${GO_IMAGE}
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && npm install --global yarn@1.22.22

ARG STUFFBIN_VERSION
RUN GOBIN=/go/bin go install github.com/knadh/stuffbin/...@${STUFFBIN_VERSION}

WORKDIR /src
ENV CYPRESS_INSTALL_BINARY=0
# 프론트 postinstall 이 ../static/public/static 로 복사한다.
RUN mkdir -p /src/static/public/static
COPY frontend/package.json frontend/yarn.lock frontend/
COPY frontend/email-builder/package.json frontend/email-builder/yarn.lock frontend/email-builder/
# 어떤 사용자로 돌려도 쓸 수 있게 한 층에서 권한을 준다(층이 둘로 늘지 않게 같은 RUN).
RUN cd frontend && yarn install --frozen-lockfile \
    && cd email-builder && yarn install --frozen-lockfile \
    && chmod -R a+rwX /src
