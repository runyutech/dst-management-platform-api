#!/bin/bash
# RGS 版入口。上游 entry-point.sh 把 DMP_HOME 写死成 /root 并自己装 steamcmd，
# 这里改成 /opt/dmp，steamcmd 已经烤进镜像，工作目录默认 /workspace。
#
# RGS/MCSM 起容器时会把启动命令的第一段当 entrypoint，也就是直接跑工作目录里的 ./dmp，
# 这个脚本只在 `docker run` 单独用的时候生效。

cd "${DMP_HOME:-/workspace}" || exit 1

# DST 的 32/64 位二进制都在自己目录里找 libcurl-gnutls.so.4，缺了会起不来。
# 游戏还没装时跳过，DMP 自己的 manual_install.sh 装完也会补一次。
link_so() {
    if [ -d dst/bin64/lib64 ] && [ ! -e dst/bin64/lib64/libcurl-gnutls.so.4 ]; then
        ln -sf /usr/lib/x86_64-linux-gnu/libcurl-gnutls.so.4 dst/bin64/lib64/libcurl-gnutls.so.4
    fi
    if [ -d dst/bin/lib32 ] && [ ! -e dst/bin/lib32/libcurl-gnutls.so.4 ]; then
        ln -sf /usr/lib/i386-linux-gnu/libcurl-gnutls.so.4 dst/bin/lib32/libcurl-gnutls.so.4
    fi
}
link_so

args=(-bind "${DMP_PORT:-80}" -dbpath "${DMP_DB:-./data}" -level "${LEVEL:-info}")
if [ "${TLS}" = "true" ]; then
    args+=(-cert "${TLS_CERT:-/etc/ssl/dmp/fullchain.pem}" -key "${TLS_KEY:-/etc/ssl/dmp/privkey.pem}")
fi

exec /opt/dmp/dmp "${args[@]}"
