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
    GOPROXY=https://goproxy.cn,https://mirrors.aliyun.com/goproxy/,direct \
    GOSUMDB=off

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags '-s -w' -v -o /out/dmp main.go

FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive \
    TZ=Asia/Shanghai \
    LANG=C.UTF-8 \
    DMP_PORT=80 \
    DMP_IN_CONTAINER=1

# screen: DMP 用 screen 起每个世界
# lib32gcc-s1 / lib32stdc++6 / libcurl4-gnutls-dev:i386: DST 是 32 位程序，缺了起不来
# libcurl4-gnutls-dev: 给 64 位 bin64/lib64/libcurl-gnutls.so.4 做软链用
RUN dpkg --add-architecture i386 \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl wget tar unzip jq tzdata rsync \
      libarchive-tools \
      screen \
      lib32gcc-s1 lib32stdc++6 \
      libcurl4-gnutls-dev libcurl4-gnutls-dev:i386 \
 && ln -fs "/usr/share/zoneinfo/$TZ" /etc/localtime \
 && echo "$TZ" > /etc/timezone \
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
