# 雨云 RGS 用的 DMP 镜像。上游 docker/Dockerfile 没有改，留着自己 docker-compose 用。
#
# 和上游的区别：
# 1. 去掉 --platform=$BUILDPLATFORM / TARGETARCH，gitea runner 上直接 docker build 就能过
# 2. dmp、steamcmd 放在 /opt/dmp，不放 /root。RGS 的启动命令会 `rm -rf /root && ln -s /workspace /root`，
#    放 /root 的二进制会连同目录一起被删掉
# 3. steamcmd 预装，安装脚本直接从镜像里拷，不用每次去 steamcdn 拉
#
# 前端产物 embedFS/dist 上游已经提交进仓库，这里只编后端，不跑 node/pnpm。

FROM golang:1.25.4 AS build

ENV CGO_ENABLED=0 \
    GOFLAGS=-mod=mod

# runner 解析国外域名经常超时，go mod download / go build 会卡在 TLS 握手等到超时。
# 先固定国内 DNS。只影响这个 build 阶段，拉基础镜像由 docker daemon 自己做，不走这里。
# /etc/resolv.conf 在 build 容器里可能是只读挂载，写不进去就沿用 runner 的 DNS，不要中断构建。
RUN (echo "nameserver 223.5.5.5"; echo "nameserver 119.29.29.29") > /etc/resolv.conf 2>/dev/null \
    || echo "resolv.conf 写不进去（只读挂载），沿用 runner 的 DNS"

# 用 go env -w 写进 $GOENV，比 ENV 更贴近 go 实际读到的值（同一个键两处都设会出告警）。
# GOPROXY 国内优先、proxy.golang.org 兜底、最后 direct；GOSUMDB 用 sum.golang.org 的国内镜像，
# go.sum 完整时不会真的去查，缺条目时也不至于直接失败。
RUN go env -w GO111MODULE=on \
 && go env -w GOSUMDB=sum.golang.google.cn \
 && go env -w GOPROXY=https://goproxy.cn,https://goproxy.io,https://proxy.golang.org,direct \
 && go env GO111MODULE GOSUMDB GOPROXY

WORKDIR /src

COPY go.mod go.sum ./
# 依赖目录挂 BuildKit 缓存，go.mod 变了也不用整份重下。
# go mod download 正常时一行都不打印，看着像卡住；每次限时 5 分钟，第三次还失败就带着提示退出。
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    for i in 1 2 3; do \
      echo "go mod download（第 $i 次）"; \
      if [ "$i" = "3" ]; then \
        echo "最后一次改走 GOSUMDB=off + goproxy.cn"; \
        GOSUMDB=off GOPROXY=https://goproxy.cn timeout 300 go mod download && break; \
      else \
        timeout 300 go mod download && break; \
      fi; \
      if [ "$i" = "3" ]; then \
        echo "go mod download 三次都失败或超时（300s/次），检查 runner 到 GOPROXY 的网络和 DNS" >&2; \
        exit 1; \
      fi; \
      sleep 5; \
    done

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    for i in 1 2 3; do \
      echo "go build（第 $i 次）"; \
      go build -ldflags '-s -w' -v -o /out/dmp main.go && break; \
      if [ "$i" = "3" ]; then \
        echo "go build 三次都失败" >&2; \
        exit 1; \
      fi; \
      sleep 5; \
    done

FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive \
    TZ=Asia/Shanghai \
    LANG=C.UTF-8 \
    DMP_PORT=80 \
    DMP_IN_CONTAINER=1

# screen: DMP 用 screen 起每个世界
# socat: 控制端给 steam 模板装 DST 时会把 127.0.0.1:80 转到区域 lancache，
#   镜像里预装好，安装脚本里的 apt-get install 就是空操作（镜像已清掉 apt lists，现场装会失败）
# lib32gcc-s1 / lib32stdc++6: DST 主体是 32 位程序，缺了起不来
# libcurl3t64-gnutls 两个架构：DST 的 bin/lib32、bin64/lib64 都要 libcurl-gnutls.so.4 做软链，
#   这个 .so.4 是 SONAME 链接，属于运行时包；装 -dev 包会多给 /usr/bin/curl-config，
#   两个架构的 -dev 会抢同一个文件，dpkg 直接报 "trying to overwrite shared /usr/bin/curl-config"
RUN dpkg --add-architecture i386 \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl wget tar unzip jq tzdata rsync \
      libarchive-tools socat \
      screen \
      lib32gcc-s1 lib32stdc++6 \
      libcurl3t64-gnutls libcurl3t64-gnutls:i386 \
 && ln -fs "/usr/share/zoneinfo/$TZ" /etc/localtime \
 && echo "$TZ" > /etc/timezone \
 && test -e /usr/lib/x86_64-linux-gnu/libcurl-gnutls.so.4 \
 && test -e /usr/lib/i386-linux-gnu/libcurl-gnutls.so.4 \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

# DMP 只认相对路径 steamcmd/、dst/、data/，所以镜像里只是把 steamcmd 备好，
# 由安装脚本拷到 /workspace。新版 steamcmd.sh 找 linux/steamcmd，bootstrap 包里只有 linux32/。
RUN mkdir -p /opt/dmp/steamcmd \
 && (curl -fsSL --retry 3 --retry-delay 2 -o /tmp/steamcmd.tar.gz \
       https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz \
     || curl -fsSL --retry 3 --retry-delay 2 -o /tmp/steamcmd.tar.gz \
       https://media.steampowered.com/client/installer/steamcmd_linux.tar.gz) \
 && tar -zxf /tmp/steamcmd.tar.gz -C /opt/dmp/steamcmd \
 && rm -f /tmp/steamcmd.tar.gz \
 && ([ -x /opt/dmp/steamcmd/linux/steamcmd ] || ln -sfn linux32 /opt/dmp/steamcmd/linux)

COPY --from=build /out/dmp /opt/dmp/dmp
COPY docker/entry-point-rgs.sh /opt/dmp/entry-point.sh

RUN chmod +x /opt/dmp/dmp /opt/dmp/entry-point.sh \
 && ln -sf /opt/dmp/dmp /usr/local/bin/dmp \
 && mkdir -p /workspace

WORKDIR /workspace

EXPOSE 80/tcp

# RGS / MCSM 会用自己的启动命令覆盖 entrypoint，这里只给 `docker run` 单独跑时兜底
ENTRYPOINT ["/opt/dmp/entry-point.sh"]
