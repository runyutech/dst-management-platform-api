package constant

import "time"

// Version 平台版本号
const Version = "v3.2.2"

// ApiVersion 接口版本号
const ApiVersion = "v3" //

// HttpTimeout HTTP请求超时时间
const HttpTimeout = 30 //

// JwtExpirationHours 登录令牌过期时间
const JwtExpirationHours = 24 * 3

// StaticCacheHours 静态资源缓存时间
const StaticCacheHours = 24 * 7

// SteamLogPath steam日志
const SteamLogPath = "Steam/logs/bootstrap_log.txt"

// DarwinSteamLogPath macos steam日志
const DarwinSteamLogPath = "steamcmd/logs/console_log.txt"

// InternetIPApi1 公网IP查询接口
const InternetIPApi1 = "http://ip-api.com/json/?lang=zh-CN"

// InternetIPApi2 公网IP查询接口
const InternetIPApi2 = "http://cip.cc"

// Darwin 系统名
const Darwin = "darwin"

// ========== 模组下载 ==========

// ModDownloadTimeout 单个模组下载的总时长上限。
// steamcmd 遇到网络问题、自更新失败或者工坊接口异常时会一直挂着不退出，
// 没有上限的话它会永久占住房间的下载锁，只有重启 DMP 才能恢复
const ModDownloadTimeout = 30 * time.Minute

// ModDownloadStallTimeout 下载目录体积持续不变多久就判定 steamcmd 卡死。
// 比总时长更重要：卡死的典型表现是进程还在、体积一动不动，没必要等满 30 分钟
const ModDownloadStallTimeout = 3 * time.Minute

// ModDownloadCheckInterval 模组下载体积检查间隔
const ModDownloadCheckInterval = 5 * time.Second

// ModCopyTimeout 下载完成后把模组拷进各世界目录、处理 acf 的超时时间
const ModCopyTimeout = 10 * time.Minute

// GameUpdateTimeout 游戏本体更新（steamcmd +app_update）的超时时间。
// steamcmd 卡住时 DstUpdating 会一直为 true，之后所有更新请求都被跳过
const GameUpdateTimeout = 30 * time.Minute

// ========== 容器端口 ==========

// EnvSurfacePort 容器传入的地面（主世界）端口。RGS 的 NAT 是 PortIn=PortOut，
// DST 只会把 server_port 原样告诉客户端，所以面板必须直接监听对外端口
const EnvSurfacePort = "DMP_SURFACE_PORT"

// EnvCavePort 容器传入的洞穴端口，不填时取地面端口 +1
const EnvCavePort = "DMP_CAVE_PORT"

// EnvConnectHost / EnvConnectPort 玩家实际用来连服的对外地址和端口。
// RGS 下机器自己探测到的出网 IP 和玩家能连的地址不是一回事（NAT 映射、域名），
// 直连代码必须用启动命令传进来的这一对，否则玩家照抄面板里的代码连不上
const (
	EnvConnectHost = "DMP_CONNECT_HOST"
	EnvConnectPort = "DMP_CONNECT_PORT"
)

// EnvAdminPassword 容器传入的管理员密码。设置后启动时按它创建/重置管理员，
// 前端详情页的「进入饥荒管理面板」会带上这个密码做一键登录
const EnvAdminPassword = "DMP_ADMIN_PASSWORD"

// EnvAdminUsername 自动创建的管理员用户名，默认 admin
const EnvAdminUsername = "DMP_ADMIN_USERNAME"

// DefaultAdminUsername 没配 EnvAdminUsername 时用的管理员用户名
const DefaultAdminUsername = "admin"

// EnvAdminPasswordMinLength / MaxLength 环境变量里管理员密码的长度限制。
// 太短等于把面板敞开，太长会被拿去做 bcrypt 白费 CPU，超出就跳过不动账号
const (
	EnvAdminPasswordMinLength = 6
	EnvAdminPasswordMaxLength = 64
)

// EnvRoomName 容器传入的房间名。RGS 的饥荒蛋把它导成 DMP_ROOM_NAME：
// 有值时实例启动如果还没有任何房间，就按默认参数直接建房并开服，玩家不用进面板点三步。
// 留空时完全没有快捷开服行为，和普通 DMP 部署一致
const EnvRoomName = "DMP_ROOM_NAME"

// EnvRoomToken 快捷建房用的饥荒游戏令牌（科雷官网申请）。
// 没配时用平台内置的默认令牌（utils.GetDstToken，和存档导入缺令牌时的兜底是同一个），
// 所以自动建出来的房间默认就在线，能出现在游戏内的服务器列表里
const EnvRoomToken = "DMP_ROOM_TOKEN"

// QuickRoomGameMode 快捷建房的游戏模式。世界配置只能对应一种模式，
// 所以这里固定成前端新建房间的默认值「无尽」，不做成变量
const QuickRoomGameMode = "endless"

// QuickRoomMaxNameLength 快捷建房房间名的最大长度（按字符算）
const QuickRoomMaxNameLength = 32
