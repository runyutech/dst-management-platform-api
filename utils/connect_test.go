package utils

import (
	"dst-management-platform-api/constant"
	"testing"
)

func TestConnectHost(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{name: "未设置", env: "", want: ""},
		{name: "纯域名", env: "zjc.rainplay.cn", want: "zjc.rainplay.cn"},
		{name: "带空格", env: "  zjc.rainplay.cn  ", want: "zjc.rainplay.cn"},
		{name: "带末尾斜杠", env: "zjc.rainplay.cn/", want: "zjc.rainplay.cn"},
		{name: "误填完整URL", env: "https://zjc.rainplay.cn/", want: "zjc.rainplay.cn"},
		{name: "IPv4", env: "43.226.59.10", want: "43.226.59.10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(constant.EnvConnectHost, c.env)
			if got := ConnectHost(); got != c.want {
				t.Fatalf("ConnectHost() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestConnectPort(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{name: "未设置", env: "", want: 0},
		{name: "非法", env: "abc", want: 0},
		{name: "越界", env: "70000", want: 0},
		{name: "正常", env: "33444", want: 33444},
		{name: "带空格", env: " 33444 ", want: 33444},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(constant.EnvConnectPort, c.env)
			if got := ConnectPort(); got != c.want {
				t.Fatalf("ConnectPort() = %d, want %d", got, c.want)
			}
		})
	}
}
