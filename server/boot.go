package server

import (
	"dst-management-platform-api/constant"
	"net/http"

	"github.com/gin-gonic/gin"
)

// 一键登录用的查询参数名。RGS 详情页的「进入饥荒管理面板」按钮会带上 pwd
const (
	bootPasswordParam = "pwd"
	bootUsernameParam = "user"
)

// bootLoginJS 返回前端启动脚本：地址里带了密码就直接登进去，省掉登录页。
//
// 上游 dist 里没有这个文件，必须在模块脚本之前、以阻塞脚本的方式加载，
// 这样 token 在 Vue 挂载之前就写进 localStorage，路由守卫能直接进 /rooms。
// 登录本身走同步 XHR：异步的话 App 会先以「未登录」状态启动，然后被重定向到登录页。
func bootLoginJS() string {
	return `(function () {
  try {
    var p = new URLSearchParams(window.location.search);
    var pwd = p.get('` + bootPasswordParam + `');
    if (!pwd) { return; }
    var user = p.get('` + bootUsernameParam + `') || '` + constant.DefaultAdminUsername + `';

    function req(method, path, body, token) {
      var x = new XMLHttpRequest();
      x.open(method, path, false);
      x.setRequestHeader('Content-Type', 'application/json');
      x.setRequestHeader('X-I18n-Lang', 'zh');
      if (token) { x.setRequestHeader('X-DMP-TOKEN', token); }
      x.send(body ? JSON.stringify(body) : null);
      return JSON.parse(x.responseText);
    }

    var login = req('POST', '/` + constant.ApiVersion + `/user/login', { username: user, password: pwd });
    if (Number(login.code) !== 200 || !login.data) {
      console.warn('[rgs] 面板一键登录失败：' + (login.message || '未知错误'));
      return;
    }
    var token = login.data;
    var base = req('GET', '/` + constant.ApiVersion + `/user/base', null, token);
    var menu = req('GET', '/` + constant.ApiVersion + `/user/menu', null, token);
    if (Number(base.code) !== 200 || Number(menu.code) !== 200) {
      console.warn('[rgs] 面板一键登录失败：读取账号信息出错');
      return;
    }
    localStorage.setItem('` + bootStorageKey + `', JSON.stringify({
      menus: menu.data || [],
      userInfo: base.data || {},
      token: token
    }));
  } catch (e) {
    console.warn('[rgs] 面板一键登录失败：' + e);
  } finally {
    // 密码不能留在地址栏和浏览历史里，登录成不成功都清掉
    try {
      if (window.location.search) {
        window.history.replaceState(null, '', window.location.pathname + window.location.hash);
      }
    } catch (e) {}
  }
})();
`
}

// bootStorageKey 前端的 pinia 持久化键（dist 里是 persist.key = 'dmp-' + 'user'）
const bootStorageKey = "dmp-user"

// registerBootRoutes 注册启动脚本和默认端口。必须放在静态资源中间件之前
func registerBootRoutes(r *gin.Engine) {
	r.GET("/default-ports.js", func(c *gin.Context) {
		// CacheControl 中间件按 .js 后缀给过 public/max-age/Expires，这里必须整段盖掉。
		// 不然浏览器会一直用旧的默认端口去建房，和 NAT 分配的号对不上
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", []byte(defaultPortsJS()))
	})

	r.GET("/rgs-boot.js", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", []byte(bootLoginJS()))
	})
}
