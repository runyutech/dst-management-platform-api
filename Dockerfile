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

# 构建机（sz-debian）在海外，直接用官方代理 + 官方 sumdb。
# 不要再指 goproxy.cn / 阿里云 / 腾讯云这些国内镜像：从海外访问会被限速，
# go 既不报错也不打印进度，就是静默卡到 timeout——之前几次卡在 go mod download 就是这个原因。
# 也不要改 /etc/resolv.conf：runner 的 DNS 本来是好用的，换成 223.5.5.5 只会更慢。
ENV CGO_ENABLED=0 \
    GO111MODULE=on \
    GOPROXY=https://proxy.golang.org,direct \
    GOSUMDB=sum.golang.org \
    GOFLAGS=-mod=mod

WORKDIR /src

# 容器里的出口可能和宿主机不一样（runner 是 docker-in-docker 时尤其如此），
# 先花最多 24 秒探一下 443。全都不通就直接退出，别在这儿静默等 5 分钟。
# 用 bash 的 /dev/tcp，不依赖 curl/wget 是否装过。
RUN ok=0; \
    for h in proxy.golang.org goproxy.cn github.com; do \
      printf '探测 %s:443 ... ' "$h"; \
      if timeout 8 bash -c "exec 3<>/dev/tcp/$h/443" 2>/dev/null; then echo 通; ok=1; else echo 不通; fi; \
    done; \
    [ "$ok" = "1" ] || { \
      echo "镜像里连不上任何外网 443：检查 docker daemon 的 DNS/出口，或在 workflow 里用 --build-arg 传 HTTP_PROXY/HTTPS_PROXY" >&2; \
      exit 1; }

COPY go.mod go.sum ./
# 依赖目录挂 BuildKit 缓存，go.mod 变了也不用整份重下。用 shared 不用 locked：
# locked 是独占锁，runner 上另一个构建卡住时会静默排队，表现和网络卡住分不清。
# -x 会把每个模块的下载命令打出来，卡在哪一条一眼就能看到（默认一行都不打印）。
RUN --mount=type=cache,target=/go/pkg/mod,sharing=shared \
    for i in 1 2 3; do \
      echo "go mod download（第 $i 次，GOPROXY=$GOPROXY）"; \
      if [ "$i" = "3" ]; then \
        echo "最后一次改走 GOPROXY=direct + GOSUMDB=off"; \
        GOSUMDB=off GOPROXY=direct timeout 180 go mod download -x && break; \
      else \
        timeout 180 go mod download -x && break; \
      fi; \
      if [ "$i" = "3" ]; then \
        echo "go mod download 三次都失败或超时（180s/次）" >&2; \
        exit 1; \
      fi; \
      sleep 5; \
    done

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod,sharing=shared \
    --mount=type=cache,target=/root/.cache/go-build,sharing=shared \
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
