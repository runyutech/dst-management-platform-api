package utils

import (
	"dst-management-platform-api/constant"
	"os"
	"strconv"
	"strings"
)

// ConnectHost 玩家实际用来连服的对外地址（RGS 的 HOST_OUT，可能是域名）。
//
// 直连代码里优先用它，而不是机器自己探测的公网 IP：NAT 之后出口 IP 和入口地址
// 常常不是同一个，玩家照抄出口 IP 连不上。没有这个环境变量时返回空，调用方回落到原逻辑
func ConnectHost() string {
	host := strings.TrimSpace(os.Getenv(constant.EnvConnectHost))
	host = strings.TrimSuffix(host, "/")
	// 防止有人把完整 URL 塞进来，直连代码里只要主机名
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	host = strings.TrimSuffix(host, "/")
	return host
}

// ConnectPort 玩家实际用来连服的对外端口（RGS 的 PORT_OUT）。
//
// 没配或非法时返回 0，调用方回落到主世界自己的 server_port
func ConnectPort() int {
	port, err := strconv.Atoi(strings.TrimSpace(os.Getenv(constant.EnvConnectPort)))
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}
