# NebulaDrive 容器镜像
#
# 三阶段构建：
#   1) frontend —— Node 构建前端，产物写进 backend/frontend_dist（go:embed 会内嵌它）
#   2) builder —— Go 编译成单个静态二进制（CGO_ENABLED=0，纯 Go SQLite，无需系统库）
#   3) runtime —— 极小 alpine 镜像，只放二进制，非 root 运行
#
# 前端 dist 仓库里已有提交版本，理论上第 1 阶段可省。但保留它能保证
# "镜像里的前端永远由当前源码构建"，避免 Source 改了忘了重建 dist 的旧问题。

# ---------- 阶段 1：前端 ----------
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
# vite.config.ts 的 outDir 指向 ../backend/frontend_dist
RUN npm run build

# ---------- 阶段 2：Go 编译 ----------
FROM golang:1.25-alpine AS builder
WORKDIR /src/backend
# 安装 git：go mod 拉某些依赖时会用到；ca-certificates：HTTPS 拉模块需要
RUN apk add --no-cache git ca-certificates
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# 用阶段 1  freshly 构建的前端覆盖（确保前端与源码一致）
COPY --from=frontend /src/backend/frontend_dist/ ./frontend_dist/
ARG VERSION=dev
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w -X main.buildVersion=${VERSION}" -o /out/nebula .

# ---------- 阶段 3：运行 ----------
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 -S nebula \
    && adduser -u 10001 -S -G nebula nebula \
    && mkdir -p /data && chown -R nebula:nebula /data
COPY --from=builder /out/nebula /usr/local/bin/nebula
USER nebula
WORKDIR /data
VOLUME ["/data"]
# 5212 是项目默认端口；-listen 用 :5212 监听所有网卡（容器内必须如此）
EXPOSE 5212
ENTRYPOINT ["/usr/local/bin/nebula"]
CMD ["-data", "/data", "-listen", ":5212"]
