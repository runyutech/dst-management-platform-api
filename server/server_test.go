package server

import (
	"strings"
	"testing"

	"dst-management-platform-api/constant"
)

// 容器注入了地面/洞穴端口时，前端拿到的必须是这两个号（RGS 的 NAT 是 PortIn=PortOut）
func TestDefaultPortsJSWithEnv(t *testing.T) {
	t.Setenv(constant.EnvSurfacePort, "33444")
	t.Setenv(constant.EnvCavePort, "33445")

	if got, want := defaultPortsJS(), `window.__DMP_DEFAULT_PORTS__={"cavePort":33445,"serverPort":33444};`; got != want {
		t.Fatalf("defaultPortsJS() = %s, want %s", got, want)
	}
}

// 只给地面口时洞穴按 RGS 的连号口 +1
func TestDefaultPortsJSOnlySurface(t *testing.T) {
	t.Setenv(constant.EnvSurfacePort, "33444")
	t.Setenv(constant.EnvCavePort, "")

	if got, want := defaultPortsJS(), `window.__DMP_DEFAULT_PORTS__={"cavePort":33445,"serverPort":33444};`; got != want {
		t.Fatalf("defaultPortsJS() = %s, want %s", got, want)
	}
}

// 洞穴口和地面口相同时不认洞穴口，避免两个世界抢同一个端口
func TestDefaultPortsJSSameCave(t *testing.T) {
	t.Setenv(constant.EnvSurfacePort, "33444")
	t.Setenv(constant.EnvCavePort, "33444")

	if got, want := defaultPortsJS(), `window.__DMP_DEFAULT_PORTS__={"cavePort":33445,"serverPort":33444};`; got != want {
		t.Fatalf("defaultPortsJS() = %s, want %s", got, want)
	}
}

// 没配环境变量（老实例的 run.sh 里没有 export）时给 0，前端退回上游默认值，
// 同时启动日志要能看出来是哪一边没配
func TestDefaultPortsJSWithoutEnv(t *testing.T) {
	t.Setenv(constant.EnvSurfacePort, "")
	t.Setenv(constant.EnvCavePort, "")

	if got, want := defaultPortsJS(), `window.__DMP_DEFAULT_PORTS__={"cavePort":0,"serverPort":0};`; got != want {
		t.Fatalf("defaultPortsJS() = %s, want %s", got, want)
	}
}

// 非法端口按没配处理，不能让 Atoi 的 0 或超界值透传到前端
func TestEnvPortInvalid(t *testing.T) {
	for _, value := range []string{"", "abc", "0", "-1", "70000", "  "} {
		t.Setenv("RGS_TEST_PORT", value)
		if got := envPort("RGS_TEST_PORT"); got != 0 {
			t.Fatalf("envPort(%q) = %d, want 0", value, got)
		}
	}

	t.Setenv("RGS_TEST_PORT", " 33444 ")
	if got := envPort("RGS_TEST_PORT"); got != 33444 {
		t.Fatalf("envPort() = %d, want 33444", got)
	}
}

// 一键登录脚本要认 ?pwd=，登录后写进前端持久化的那个键，并且把密码从地址栏清掉
func TestBootLoginJS(t *testing.T) {
	js := bootLoginJS()

	for _, want := range []string{
		"p.get('" + bootPasswordParam + "')",
		"/" + constant.ApiVersion + "/user/login",
		"/" + constant.ApiVersion + "/user/base",
		"/" + constant.ApiVersion + "/user/menu",
		"localStorage.setItem('" + bootStorageKey + "'",
		"history.replaceState",
		// 同步请求：异步的话 App 会先按未登录启动
		"x.open(method, path, false)",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("bootLoginJS() 缺少 %q", want)
		}
	}
}
