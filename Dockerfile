# Why/为什么: 构建阶段分两步——oven/bun 先产出 frontends/app/dist（go:embed 的
# 编译前置，dist 被 .dockerignore 排除必须现场构建），golang:1.25-alpine 再
# 产出纯 Go 静态二进制；运行阶段只保留 alpine + tzdata 的时区逻辑。发布流水线
# 的预编译二进制镜像仍由 packaging/docker/Dockerfile 负责。
# English: two build stages — oven/bun produces frontends/app/dist (the
# go:embed compile prerequisite; dist is excluded by .dockerignore on
# purpose), then golang:1.25-alpine builds a static pure-Go binary; the
# runtime stage keeps only alpine plus the tzdata-based TZ handling. Release
# pipelines keep using the prebuilt-binary image from packaging/docker/Dockerfile.

FROM oven/bun:1-alpine AS frontend

# Why/为什么: 默认走官方 npm registry；受限网络可用 --build-arg NPM_REGISTRY=https://registry.npmmirror.com 覆盖。
# English: defaults to the official npm registry; restricted networks can
# override with --build-arg NPM_REGISTRY=https://registry.npmmirror.com.
ARG NPM_REGISTRY=""

WORKDIR /fe

COPY frontends/app/package.json frontends/app/bun.lock ./
RUN if [ -n "${NPM_REGISTRY}" ]; then export NPM_CONFIG_REGISTRY="${NPM_REGISTRY}"; fi \
    && bun install --frozen-lockfile

COPY frontends/app ./
RUN bun run build \
    && test -f dist/index.html

FROM golang:1.25-alpine AS build

# Why/为什么: 默认走官方 proxy.golang.org；受限网络可用 --build-arg GOPROXY=https://goproxy.cn 覆盖。
# English: defaults to the official proxy.golang.org; restricted networks can
# override with --build-arg GOPROXY=https://goproxy.cn.
ARG GOPROXY=""

WORKDIR /src

COPY go.mod go.sum ./
RUN if [ -n "${GOPROXY}" ]; then go env -w GOPROXY="${GOPROXY}"; fi \
    && go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY embed.go ./
COPY config.yml ./
COPY --from=frontend /fe/dist ./frontends/app/dist

# Why/为什么: CGO_ENABLED=0 产出静态二进制，与发布矩阵的交叉编译命令完全一致。
# English: CGO_ENABLED=0 produces a static binary, matching the release
# matrix cross-compile command exactly.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/ikuai-bypass ./cmd/ikuai-bypass

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

ENV TZ=Asia/Shanghai

WORKDIR /opt/ikuai-bypass

COPY --from=build /out/ikuai-bypass /usr/local/bin/ikuai-bypass
COPY packaging/docker/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY config.yml /opt/ikuai-bypass/config.yml

RUN mkdir -p /etc/ikuai-bypass \
    && chmod +x /usr/local/bin/ikuai-bypass /usr/local/bin/docker-entrypoint.sh

VOLUME ["/etc/ikuai-bypass"]

EXPOSE 19001

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["-r", "cron"]
