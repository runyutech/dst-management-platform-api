package room

import (
	_ "embed"
	"os"
	"strings"
	"unicode/utf8"

	"dst-management-platform-api/constant"
	"dst-management-platform-api/database/dao"
	"dst-management-platform-api/database/models"
	"dst-management-platform-api/dst"
	"dst-management-platform-api/logger"
	"dst-management-platform-api/utils"
)

// 快捷建房用的默认世界配置，取自 DMP 前端「新建房间 → 无尽」的初始 leveldataoverride.lua。
// 正常流程是前端把这份 Lua 拼好随请求提交，容器里没有前端参与，所以服务端得自己留一份。
// 上游前端更新默认配置后需要同步这里（assets 目录下两个文件）。
//
//go:embed assets/leveldata_endless_master.lua
var quickMasterLevelData string

//go:embed assets/leveldata_endless_caves.lua
var quickCavesLevelData string

// 快捷建房的默认值，全部取自前端新建房间时的初始值：
// rooms 部分见前端 room 组件的 form 初始值，settings 部分见 roomSetting 组件的初始值，
// 端口偏移见 config-*.js 的 serverPort / masterPort / masterServerPort / authenticationPort。
// 唯一的不同是地面/洞穴端口：RGS 会用自己的 DMP_SURFACE_PORT / DMP_CAVE_PORT 覆盖。
const (
	quickDefaultServerPort         = 11000
	quickDefaultMasterPort         = 21000
	quickDefaultMasterServerPort   = 31000
	quickDefaultAuthenticationPort = 41000
	quickDefaultMaxPlayer          = 6
	quickDefaultMaxRollBack        = 10
	quickDefaultTickRate           = 15
	quickDefaultStartType          = "64-bit"
	quickMasterGameID              = 101
	quickCavesGameID               = 102
	quickMasterWorldName           = "Master"
	quickCavesWorldName            = "Caves"
	quickDefaultBackupSetting      = `[{"time":"06:00:00"}]`
	quickDefaultResetSetting       = `{"force":false,"time":"06:10:00","days":3}`
	quickDefaultScheduledSetting   = `{"start":"07:00:00","stop":"01:00:00"}`
)

// EnsureQuickRoom 按容器传入的房间名做「快捷开服」。
//
// RGS 的 dst_dmp 蛋把蛋变量「房间名」导成 DMP_ROOM_NAME。填了它意味着玩家希望开机就能进服，
// 不想进面板点「新建房间 → 一步步填参数 → 启动」三轮表单。
//
// 行为：
//   - 变量为空：什么都不做，和原来的 DMP 部署完全一致；
//   - 还没有任何房间：按前端新建房间的默认参数建一个同名房间、写配置文件并直接开服；
//   - 已经有房间：不重复建房（避免用户改名后越建越多），只在同名房间库里仍是开启状态时补一次启动
//     （容器重启后 screen 全没了，但库里的 status 还是 true，用户语义上并没有停过服）；
//   - 同名房间在库里是关闭状态（用户自己在面板里点过关闭）：尊重这个状态，不启动。
func EnsureQuickRoom(roomDao *dao.RoomDAO, worldDao *dao.WorldDAO, roomSettingDao *dao.RoomSettingDAO) {
	name, ok := quickRoomName()
	if !ok {
		return
	}

	rooms, err := roomDao.GetRoomBasic()
	if err != nil {
		logger.Logger.Errorf("快捷开服：查询房间列表失败, err: %v", err)
		return
	}

	if len(*rooms) == 0 {
		createQuickRoom(name, roomDao, worldDao, roomSettingDao)
		return
	}

	for _, r := range *rooms {
		if r.RoomName != name {
			continue
		}
		if !r.Status {
			logger.Logger.Infof("快捷开服：房间「%s」处于关闭状态，跳过启动", name)
			return
		}
		startQuickRoom(r.RoomID, name)
		return
	}

	logger.Logger.Infof("快捷开服：已有 %d 个房间，没有名为「%s」的房间，跳过且不重复建房（想重新快捷建房请先删掉现有房间）",
		len(*rooms), name)
}

// quickRoomName 读取并清洗房间名。没配或者清洗后为空都返回 false
func quickRoomName() (string, bool) {
	raw := os.Getenv(constant.EnvRoomName)
	// 房间名会写进 cluster.ini 的 cluster_name，换行会把配置项劈成两半，直接去掉
	name := strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', 0:
			return -1
		}
		return r
	}, raw)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	if utf8.RuneCountInString(name) > constant.QuickRoomMaxNameLength {
		runes := []rune(name)
		name = strings.TrimSpace(string(runes[:constant.QuickRoomMaxNameLength]))
		logger.Logger.Warnf("快捷开服：房间名超过 %d 个字符，已截断为「%s」", constant.QuickRoomMaxNameLength, name)
	}
	return name, true
}

// createQuickRoom 按默认参数建房间、写配置文件并启动游戏。
// 只有在「一个房间都没有」时才会走到这里，所以端口固定按第 1 个房间算
func createQuickRoom(name string, roomDao *dao.RoomDAO, worldDao *dao.WorldDAO, roomSettingDao *dao.RoomSettingDAO) {
	room, worlds, setting := buildQuickRoom(name)

	created, err := roomDao.CreateRoom(&room)
	if err != nil {
		logger.Logger.Errorf("快捷开服：创建房间失败, err: %v", err)
		return
	}

	for i := range worlds {
		worlds[i].RoomID = created.ID
		if err := worldDao.Create(&worlds[i]); err != nil {
			logger.Logger.Errorf("快捷开服：创建世界「%s」失败, err: %v", worlds[i].WorldName, err)
			return
		}
	}

	setting.RoomID = created.ID
	if err := roomSettingDao.Create(&setting); err != nil {
		logger.Logger.Errorf("快捷开服：创建房间设置失败, err: %v", err)
		return
	}

	game := dst.NewGameController(created, &worlds, &setting, "zh")
	if err := game.SaveAll(); err != nil {
		// 配置没写全时游戏起来也是错的，直接放弃启动，让用户在面板里看到房间但处于关闭状态
		logger.Logger.Errorf("快捷开服：房间「%s」写入配置文件失败, err: %v", name, err)
		return
	}

	if err := game.StartAllWorld(); err != nil {
		logger.Logger.Errorf("快捷开服：房间「%s」已创建但启动游戏失败, err: %v；可在面板里手动启动", name, err)
		return
	}

	// 启动成功才置为开启，失败时房间里仍是「关闭」，用户可以在面板里重试
	created.Status = true
	if err := roomDao.UpdateRoom(created); err != nil {
		logger.Logger.Errorf("快捷开服：更新房间状态失败, err: %v", err)
	}

	logger.Logger.Infof("快捷开服：已按默认参数创建房间「%s」并启动游戏（地面端口 %d，洞穴端口 %d）",
		name, worlds[0].ServerPort, worlds[1].ServerPort)
}

// startQuickRoom 已有同名房间时补一次启动。StartAllWorld 会跳过还活着的世界，重复调用是安全的
func startQuickRoom(roomID int, name string) {
	room, worlds, setting, err := dao.FetchGameInfo(roomID)
	if err != nil {
		logger.Logger.Errorf("快捷开服：读取房间「%s」配置失败, err: %v", name, err)
		return
	}

	game := dst.NewGameController(room, worlds, setting, "zh")
	if err := game.StartAllWorld(); err != nil {
		logger.Logger.Errorf("快捷开服：启动房间「%s」失败, err: %v；可在面板里手动启动", name, err)
		return
	}

	logger.Logger.Infof("快捷开服：房间「%s」已启动", name)
}

// buildQuickRoom 组装默认的房间、世界和房间设置
func buildQuickRoom(name string) (models.Room, []models.World, models.RoomSetting) {
	// 端口按「第几个房间」错开，和前端 roomCount + 1 的算法一致（这里只有第 1 个房间）；
	// RGS 的 NAT 是 PortIn=PortOut，地面/洞穴必须直接监听对外端口
	const index = 1
	surface, cave, ok := envGamePorts()
	if !ok {
		surface = quickDefaultServerPort + index
		cave = surface + 1
	}

	token := strings.TrimSpace(os.Getenv(constant.EnvRoomToken))
	if token == "" {
		// 没单独配就用平台内置的默认令牌：前端新建房间时那个「获取令牌」按钮填的也是它，
		// 存档导入缺 cluster_token.txt 时同样是这个兜底。有令牌房间才是在线的，
		// 否则 offline_cluster=true，只能 c_connect 直连，不会出现在服务器列表里
		token = utils.GetDstToken()
	}

	room := models.Room{
		Status:      false,
		GameName:    name,
		GameMode:    constant.QuickRoomGameMode,
		MaxPlayer:   quickDefaultMaxPlayer,
		MaxRollBack: quickDefaultMaxRollBack,
		// 前端新建房间时 modInOne 默认为 true，模组配置统一放在房间维度
		ModInOne:   true,
		ModData:    "return {}",
		PauseEmpty: true,
		Token:      token,
		// 有令牌就是在线的：会登录科雷的服务器列表，玩家能在游戏里搜到房间
		Offline:    false,
		MasterIP:   "127.0.0.1",
		MasterPort: quickDefaultMasterPort + index,
		ClusterKey: utils.RandomString(14),
	}

	worlds := []models.World{
		{
			GameID:             quickMasterGameID,
			WorldName:          quickMasterWorldName,
			ServerPort:         surface,
			MasterServerPort:   quickDefaultMasterServerPort + index,
			AuthenticationPort: quickDefaultAuthenticationPort + index,
			IsMaster:           true,
			EncodeUserPath:     true,
			LevelData:          quickMasterLevelData,
		},
		{
			GameID:             quickCavesGameID,
			WorldName:          quickCavesWorldName,
			ServerPort:         cave,
			MasterServerPort:   quickDefaultMasterServerPort + index + 1,
			AuthenticationPort: quickDefaultAuthenticationPort + index + 1,
			IsMaster:           false,
			EncodeUserPath:     true,
			LevelData:          quickCavesLevelData,
		},
	}

	setting := models.RoomSetting{
		// 备份默认开启（每天 06:00），和前端新建房间的初始值一致
		BackupEnable:              true,
		BackupSetting:             quickDefaultBackupSetting,
		BackupCleanEnable:         false,
		BackupCleanSetting:        30,
		RestartEnable:             false,
		RestartSetting:            "06:30:00",
		ResetEnable:               false,
		ResetSetting:              quickDefaultResetSetting,
		AnnounceSetting:           "[]",
		KeepaliveEnable:           false,
		KeepaliveSetting:          30,
		ScheduledStartStopEnable:  false,
		ScheduledStartStopSetting: quickDefaultScheduledSetting,
		PlayerUpdateModEnable:     false,
		RoomMetricsEnable:         false,
		TickRate:                  quickDefaultTickRate,
		StartType:                 quickDefaultStartType,
		WebhookSetting:            "[]",
	}

	return room, worlds, setting
}
