package dst

import (
	"context"
	"dst-management-platform-api/cache"
	"dst-management-platform-api/constant"
	"dst-management-platform-api/logger"
	"dst-management-platform-api/utils"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yuin/gopher-lua"
)

var (
	// 每个房间一把锁：同一房间的模组下载共用 acf 和 ugc 目录，必须串行；
	// 不同房间互不影响，一个房间的 steamcmd 卡死不会把其它房间堵住
	modDownloadLocksMu sync.Mutex
	modDownloadLocks   = map[int]*sync.Mutex{}

	// 正在下载的模组，key 为「房间ID/模组ID」。
	// 下载状态缓存只有进度，没有「到底还在不在跑」的信息：
	// DMP 重启、看门狗终止、拷贝失败都会留下没清干净的记录，
	// handler 靠这个集合区分「真在下载」和「残留状态」，避免永远提示下载中
	modDownloadStateMu sync.Mutex
	modDownloadRunning = map[string]struct{}{}
)

type modSaveData struct {
	ugcPath    string
	notUgcPath string
}

func modDownloadKey(roomID, modID int) string {
	return strconv.Itoa(roomID) + "/" + strconv.Itoa(modID)
}

// lockModDownload 取到房间级的下载锁，返回解锁函数
func lockModDownload(roomID int) func() {
	modDownloadLocksMu.Lock()
	lock, ok := modDownloadLocks[roomID]
	if !ok {
		lock = &sync.Mutex{}
		modDownloadLocks[roomID] = lock
	}
	modDownloadLocksMu.Unlock()

	lock.Lock()
	return lock.Unlock
}

// TryStartModDownload 占用某个模组的下载槽，返回 false 表示已经在下载中
func TryStartModDownload(roomID, modID int) bool {
	modDownloadStateMu.Lock()
	defer modDownloadStateMu.Unlock()

	key := modDownloadKey(roomID, modID)
	if _, ok := modDownloadRunning[key]; ok {
		return false
	}
	modDownloadRunning[key] = struct{}{}
	return true
}

// FinishModDownload 释放下载槽
func FinishModDownload(roomID, modID int) {
	modDownloadStateMu.Lock()
	defer modDownloadStateMu.Unlock()
	delete(modDownloadRunning, modDownloadKey(roomID, modID))
}

// ModDownloadRunning 该模组是否真的有下载在跑
func ModDownloadRunning(roomID, modID int) bool {
	modDownloadStateMu.Lock()
	defer modDownloadStateMu.Unlock()
	_, ok := modDownloadRunning[modDownloadKey(roomID, modID)]
	return ok
}

func (g *Game) dsModsSetup() error {
	g.roomMutex.Lock()
	defer g.roomMutex.Unlock()

	var modData string
	if g.room.ModInOne {
		modData = g.room.ModData
	} else {
		modData = g.worldSaveData[0].ModData
	}

	L := utils.NewSafeLuaState()
	defer L.Close()
	if err := L.DoString(modData); err != nil {
		logger.Logger.Warnf("解析模组配置失败，可能含有lua注入，终止：%v", err)
		return fmt.Errorf("解析模组配置失败，可能含有lua注入，终止：%v", err)
	}
	modsTable := L.Get(-1)
	fileContent := ""
	if tbl, ok := modsTable.(*lua.LTable); ok {
		// 有配置，但为空
		if tbl.Len() == 0 {
			err := utils.TruncAndWriteFile(getGameModSettingPath(), fileContent)
			if err != nil {
				return err
			}
		}
		tbl.ForEach(func(key lua.LValue, value lua.LValue) {
			// 检查键是否是字符串，并且以 "workshop-" 开头
			if strKey, ok := key.(lua.LString); ok && strings.HasPrefix(string(strKey), "workshop-") {
				// 提取 "workshop-" 后面的数字
				workshopID := strings.TrimPrefix(string(strKey), "workshop-")
				fileContent = fileContent + "ServerModSetup(\"" + workshopID + "\")\n"
			}
		})
		// 有配置，不为空
		err := utils.TruncAndWriteFile(getGameModSettingPath(), fileContent)
		if err != nil {
			return err
		}
	} else {
		// 无配置
		err := utils.TruncAndWriteFile(getGameModSettingPath(), fileContent)
		if err != nil {
			return err
		}
	}

	return nil
}

func getGameModSettingPath() string {
	if cache.OsType == constant.Darwin {
		return constant.DarwinGameModSettingPath
	} else {
		return constant.GameModSettingPath
	}
}

func (g *Game) downloadMod(id int, fileURL string) (error, int64) {
	ugc := fileURL == ""

	atomic.AddInt32(&cache.ModDownloadExecuting, 1)
	defer atomic.AddInt32(&cache.ModDownloadExecuting, -1)

	unlock := lockModDownload(g.room.ID)
	defer unlock()

	stopMonitor := g.monitorModDownload(id, ugc)
	defer stopMonitor()

	var (
		err     error
		modSize int64
	)

	if ugc {
		// 1. ugc mod 统一下载到 dmp_files/ugc, 也就是dmp_files/ugc/{cluster}/steamapps/workshop{appworkshop_322330.acf  content  downloads}
		// 2. 下载完成后，将下载的mod文件全部移动至{ugcPath}/{cluster}/{worlds}/ 删除-复制
		// 3. 读取游戏acf文件和dmp_files的acf文件，更新当前mod-id所对应的所有字段

		// 1
		logger.Logger.Debugf("正在下载模组：%d", id)
		downloadCmd := g.generateModDownloadCmd(id)
		logger.Logger.Debug(downloadCmd)
		err = g.runSteamcmdDownload(id, downloadCmd)
		if err != nil {
			logger.Logger.Errorf("下载模组失败, err: %v", err)
			return err, modSize
		}
		time.Sleep(500 * time.Millisecond)

		// steamcmd 偶尔会正常退出但什么都没下下来，这里先确认落盘，
		// 否则后面拷空目录、读 acf 都会失败，报错还看不出原因
		if !utils.FileDirectoryExists(fmt.Sprintf("%s/mods/ugc/%s/steamapps/workshop/content/322330/%d", constant.DmpFiles, g.clusterName, id)) {
			err = fmt.Errorf("steamcmd 没有下载到模组 %d 的文件", id)
			logger.Logger.Errorf("%v", err)
			return err, modSize
		}

		// 2
		logger.Logger.Debugf("正在移动模组：%d", id)
		err = g.removeGameOldMod(id)
		if err != nil {
			logger.Logger.Errorf("移动模组失败, err: %v", err)
			return err, modSize
		}
		copyCmd := g.generateModCopyCmd(id)
		logger.Logger.Debug(copyCmd)
		err = utils.BashCMDTimeout(constant.ModCopyTimeout, copyCmd)
		if err != nil {
			logger.Logger.Errorf("移动模组失败, err: %v", err)
			return err, modSize
		}
		time.Sleep(500 * time.Millisecond)

		// 3
		gameAcfPath := fmt.Sprintf("%s/%s/appworkshop_322330.acf", g.ugcPath, g.worldSaveData[0].WorldName)
		logger.Logger.Debugf("正在处理acf文件：%s", gameAcfPath)
		gameAcfContent, err := utils.ReadLinesToSlice(gameAcfPath)
		if err != nil {
			gameAcfContent = []string{}
		}
		err = g.processAcf(id)
		if err != nil {
			logger.Logger.Errorf("修改acf文件失败, err: %v", err)
			// 下载失败就恢复下载前的acf文件
			logger.Logger.Info("正在恢复旧的acf文件")
			for _, world := range g.worldSaveData {
				gameAcfPath = fmt.Sprintf("%s/%s/appworkshop_322330.acf", g.ugcPath, world.WorldName)
				writeErr := utils.WriteLinesFromSlice(gameAcfPath, gameAcfContent)
				if writeErr != nil {
					logger.Logger.Errorf("恢复acf文件失败, err: %v", writeErr)
				}
			}

			return err, modSize
		}
		time.Sleep(500 * time.Millisecond)

		modSize, err = utils.GetDirSize(fmt.Sprintf("%s/%s/content/322330/%d", g.ugcPath, g.worldSaveData[0].WorldName, id))
		logger.Logger.Debugf("模组路径为%s", fmt.Sprintf("%s/%s/content/322330/%d", g.ugcPath, g.worldSaveData[0].WorldName, id))
		logger.Logger.Debugf("模组大小为%d", modSize)
		if err != nil {
			logger.Logger.Errorf("获取模组大小失败, err: %v", err)
			return err, modSize
		}

	} else {
		// 1. 下载zip文件并保存
		// 2. 解压zip文件至{g.notUgcPath}/workshop-id
		err, modSize = downloadNotUGCMod(fileURL, id, g.notUgcPath)
		if err != nil {
			logger.Logger.Errorf("下载mod失败, err: %v", err)
			return err, modSize
		}
	}

	return nil, modSize
}

func (g *Game) generateModDownloadCmd(id int) string {
	// @ShutdownOnFailedCommand / @NoPromptForPassword 必须写在 +login 前面，
	// 否则工坊接口报错或要密码时 steamcmd 会停在交互提示上，等到天荒地老
	return fmt.Sprintf("steamcmd/steamcmd.sh +@ShutdownOnFailedCommand 1 +@NoPromptForPassword 1 +force_install_dir %s/%s/mods/ugc/%s +login anonymous +workshop_download_item 322330 %d +quit", cache.CurrentDir, constant.DmpFiles, g.clusterName, id)
}

// runSteamcmdDownload 执行工坊模组下载，带总超时和「体积不增长」看门狗。
//
// 1% 进度是前端占位符，不能拿来判断下载是否在动；这里只用下载目录的真实体积。
// 一旦体积长时间不变就判定 steamcmd 卡死，取消并杀掉整个进程组，
// 保证房间下载锁能释放、下载状态能清掉，玩家点重试就能重新下。
func (g *Game) runSteamcmdDownload(id int, cmd string) error {
	// 钉住内容服务器区域，避免被分到不稳定的 CDN 卡在 0%
	EnsureSteamCellID()

	ctx, cancel := context.WithTimeout(context.Background(), constant.ModDownloadTimeout)
	defer cancel()

	var stalled atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)

		ticker := time.NewTicker(constant.ModDownloadCheckInterval)
		defer ticker.Stop()

		lastSize := int64(-1)
		lastChange := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				size := g.ugcDownloadBytes(id)
				if size != lastSize {
					lastSize = size
					lastChange = now
					continue
				}
				if now.Sub(lastChange) < constant.ModDownloadStallTimeout {
					continue
				}
				stalled.Store(true)
				logger.Logger.Warnf("模组 %d 下载 %d 秒没有进度，判定 steamcmd 卡死，终止下载",
					id, int(now.Sub(lastChange).Seconds()))
				cancel()
				return
			}
		}
	}()

	err := utils.BashCMDContext(ctx, cmd)
	cancel()
	<-done

	if stalled.Load() {
		return fmt.Errorf("steamcmd 下载模组 %d 卡死（%d 秒没有进度），已终止",
			id, int(constant.ModDownloadStallTimeout.Seconds()))
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("steamcmd 下载模组 %d 超时（超过 %d 分钟），已终止",
			id, int(constant.ModDownloadTimeout.Minutes()))
	}

	return err
}

// ugcDownloadBytes 返回 SteamCMD 下载目录中模组当前占用的字节数。
func (g *Game) ugcDownloadBytes(id int) int64 {
	paths := []string{
		fmt.Sprintf("%s/mods/ugc/%s/steamapps/workshop/downloads/322330/%d", constant.DmpFiles, g.clusterName, id),
		fmt.Sprintf("%s/mods/ugc/%s/steamapps/workshop/temp/322330/%d", constant.DmpFiles, g.clusterName, id),
	}
	var largest int64
	for _, path := range paths {
		size, err := utils.GetDirSize(path)
		if err == nil && size > largest {
			largest = size
		}
	}
	return largest
}

func (g *Game) monitorModDownload(id int, ugc bool) func() {
	if cache.ModDownloadStatus == nil {
		return func() {}
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				g.updateModDownloadStatus(id, ugc)
			case <-stop:
				return
			}
		}
	}()

	return func() {
		close(stop)
		<-done
	}
}

func (g *Game) updateModDownloadStatus(id int, ugc bool) {
	item, err := cache.ModDownloadStatus.Get(g.room.ID, id)
	if err != nil {
		return
	}

	var currentSize int64
	if ugc {
		currentSize = g.ugcDownloadBytes(id)
	} else {
		currentSize, err = utils.GetFileSize(fmt.Sprintf("%s/%d.zip", g.notUgcPath, id))
		if err != nil {
			currentSize = 0
		}
	}

	_ = cache.ModDownloadStatus.Set(g.room.ID, &cache.ModItem{
		ID:          id,
		Size:        item.Size,
		CurrentSize: modDownloadSizeToInt(currentSize),
	})
}

func modDownloadSizeToInt(size int64) int {
	if size <= 0 {
		return 0
	}
	maxInt := int64(^uint(0) >> 1)
	if size > maxInt {
		return int(maxInt)
	}
	return int(size)
}

func (g *Game) removeGameOldMod(id int) error {
	for _, world := range g.worldSaveData {
		path := fmt.Sprintf("%s/%s/content/322330/%d", g.ugcPath, world.WorldName, id)
		err := utils.RemoveDir(path)
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Game) generateModCopyCmd(id int) string {
	if len(g.worldSaveData) == 0 {
		return ""
	}

	dmpPath := fmt.Sprintf("%s/mods/ugc/%s/steamapps/workshop/content/322330/%d", constant.DmpFiles, g.clusterName, id)

	var cmds []string

	// 生成 复制 命令，world.WorldName来自数据库或存档，使用ShellQuote避免被bash解释
	for _, world := range g.worldSaveData {
		gamePath := fmt.Sprintf("%s/%s/content/322330/%d", g.ugcPath, world.WorldName, id)
		cmd := fmt.Sprintf("mkdir -p %s", utils.ShellQuote(fmt.Sprintf("%s/%s/content/322330", g.ugcPath, world.WorldName)))
		cmds = append(cmds, cmd)
		cmd = fmt.Sprintf("cp -r %s %s", utils.ShellQuote(dmpPath), utils.ShellQuote(gamePath))
		cmds = append(cmds, cmd)
	}

	return strings.Join(cmds, " && ")
}

func (g *Game) processAcf(id int) error {
	acfModID := strconv.Itoa(id)

	dmpAcfPath := fmt.Sprintf("%s/mods/ugc/%s/steamapps/workshop/appworkshop_322330.acf", constant.DmpFiles, g.clusterName)
	gameAcfPath := fmt.Sprintf("%s/%s/appworkshop_322330.acf", g.ugcPath, g.worldSaveData[0].WorldName)

	err := utils.EnsureFileExists(gameAcfPath)
	if err != nil {
		logger.Logger.Errorf("EnsureFileExists失败, path: %v", gameAcfPath)
		return err
	}

	dmpAcfParser, err := utils.NewParser(dmpAcfPath)
	if err != nil {
		return err
	}

	gameAcfContent, err := os.ReadFile(gameAcfPath)
	if err != nil {
		return err
	}

	var writtenContent string

	if len(gameAcfContent) == 0 {
		// 如果游戏mod目录没有acf文件，直接使用dmp下载的acf文件
		writtenContent = strings.Join(dmpAcfParser.Format(), "\n")
	} else {
		// 如果游戏mod目录含有acf文件，处理游戏acf文件
		gameAcfParser, err := utils.NewParser(gameAcfPath)
		if err != nil {
			return err
		}

		newMod, err := dmpAcfParser.GetWorkshopItemsInstalled(acfModID)
		if err != nil {
			return err
		}

		err = gameAcfParser.AddWorkshopItemsInstalled(newMod)
		if err != nil {
			return err
		}

		writtenContent = strings.Join(gameAcfParser.Format(), "\n")
	}

	for _, world := range g.worldSaveData {
		gameAcfPath = fmt.Sprintf("%s/%s/appworkshop_322330.acf", g.ugcPath, world.WorldName)
		err = utils.EnsureDirExists(fmt.Sprintf("%s/%s", g.ugcPath, world.WorldName))
		if err != nil {
			return err
		}
		err = utils.TruncAndWriteFile(gameAcfPath, writtenContent)
		if err != nil {
			return err
		}
	}

	return nil
}

type DownloadedMod struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	LocalSize  string `json:"localSize"`
	ServerSize string `json:"serverSize"`
	FileURL    string `json:"file_url"`
	PreviewURL string `json:"preview_url"`
}

func (g *Game) getDownloadedMods() *[]DownloadedMod {
	var downloadedMods []DownloadedMod

	// 获取非ugc
	modDirs, err := utils.GetDirs(g.notUgcPath, false)
	for _, dir := range modDirs {
		if strings.HasPrefix(dir, "workshop") {
			parts := strings.Split(dir, "-")
			if len(parts) == 2 {
				idStr := parts[len(parts)-1]
				id, err := strconv.Atoi(idStr)
				if err == nil {
					downloadedMods = append(downloadedMods, DownloadedMod{
						ID:        id,
						LocalSize: "0",
					})
				}
			}
		}
	}

	// 获取ugc
	// 统一模组配置下各分世界共享同一套模组，但导入存档或游戏服务端自行下载可能只
	// 填充了部分分世界目录，这里回退到实际存在模组目录的分世界，避免固定读第一个分
	// 世界(Master)时因目录缺失导致 EnsureFileExists 失败、获取已下载模组失败。
	gameAcfPath := fmt.Sprintf("%s/%s/appworkshop_322330.acf", g.ugcPath, g.resolveUgcWorld("content/322330"))
	err = utils.EnsureFileExists(gameAcfPath)
	if err != nil {
		logger.Logger.Errorf("EnsureFileExists失败, path: %v", gameAcfPath)
		return &downloadedMods
	}

	gameAcfParser, err := utils.NewParser(gameAcfPath)
	if err != nil {
		logger.Logger.Warnf("获取acf文件失败：%v", err)
		return &downloadedMods
	}

	mods := gameAcfParser.ListWorkshopItemsInstalled()
	for _, mod := range mods {
		id, err := strconv.Atoi(mod.Name)
		if err != nil {
			id = 0
		}

		downloadedMods = append(downloadedMods, DownloadedMod{
			ID:        id,
			LocalSize: mod.List["size"],
		})
	}

	return &downloadedMods
}

// resolveUgcWorld 按 worldSaveData 顺序返回 ugc 目录下第一个存在相对路径 rel 的分世界名。
//
// 统一模组配置(ModInOne)下各分世界共享同一套模组，但导入存档或游戏服务端自行下载
// 模组时可能只填充了部分分世界目录（例如只有 Caves 而没有 Master）。若固定读第一个
// 分世界，会因目录/文件缺失导致“获取模组配置失败”。这里返回第一个实际存在 rel 的分
// 世界（因此第一个分世界存在时仍优先使用它）；若都不存在则回退到第一个分世界名，保持
// 原有报错语义。rel 可为目录(如 "content/322330")或具体文件(如
// "content/322330/<id>/modinfo.lua")。
func (g *Game) resolveUgcWorld(rel string) string {
	if len(g.worldSaveData) == 0 {
		return ""
	}
	for _, world := range g.worldSaveData {
		if utils.FileDirectoryExists(fmt.Sprintf("%s/%s/%s", g.ugcPath, world.WorldName, rel)) {
			return world.WorldName
		}
	}
	return g.worldSaveData[0].WorldName
}

func (g *Game) getModConfigureOptions(worldID, modID int, ugc bool) (*[]ConfigurationOption, error) {
	var modinfoLuaPath string
	if g.room.ModInOne {
		if ugc {
			// 统一模组配置下各分世界共享同一套模组，但导入存档或游戏服务端自行下载可能
			// 只填充了部分分世界目录，这里回退到实际含有该模组的分世界，避免固定读第一个
			// 分世界(Master)时因 modinfo.lua 缺失导致获取模组配置失败。
			modinfoLuaPath = fmt.Sprintf("%s/%s/content/322330/%d/modinfo.lua", g.ugcPath, g.resolveUgcWorld(fmt.Sprintf("content/322330/%d/modinfo.lua", modID)), modID)
		} else {
			modinfoLuaPath = fmt.Sprintf("%s/workshop-%d/modinfo.lua", g.notUgcPath, modID)
		}
	} else {
		if ugc {
			var wi int
			for index, world := range g.worldSaveData {
				if worldID == world.ID {
					wi = index
					break
				}
			}
			modinfoLuaPath = fmt.Sprintf("%s/%s/content/322330/%d/modinfo.lua", g.ugcPath, g.worldSaveData[wi].WorldName, modID)
		} else {
			modinfoLuaPath = fmt.Sprintf("%s/workshop-%d/modinfo.lua", g.notUgcPath, modID)
		}
	}

	parser, err := NewModInfoParser(modinfoLuaPath, modID)
	if err != nil {
		logger.Logger.Errorf("读取modinfo文件失败, err: %v", err)
		return parser.Configuration, err
	}

	err = parser.Parse(g.lang)
	if err != nil {
		logger.Logger.Errorf("解析modinfo文件失败, err: %v", err)
		return parser.Configuration, err
	}

	return parser.Configuration, nil
}

func (g *Game) getModConfigureOptionsValues(worldID, modID int, ugc bool) (*ModORConfig, error) {
	modORParser := NewModORParser()
	defer modORParser.close()

	logger.Logger.Debugf("ugc is %t", ugc)

	var modORContent string
	if g.room.ModInOne {
		modORContent = g.room.ModData
	} else {
		world, err := g.getWorldByID(worldID)
		if err != nil {
			logger.Logger.Debugf("这里出问题?, err: %v", err)
			return &ModORConfig{}, err
		}
		modORContent = world.ModData
	}

	mods, err := modORParser.Parse(modORContent, g.lang)
	if err != nil {
		logger.Logger.Debugf("这里出问题?, err: %v", err)
		return &ModORConfig{}, err
	}

	for key, mod := range mods {
		modKey := fmt.Sprintf("workshop-%d", modID)
		if key == modKey {
			return mod, nil
		}
	}

	return &ModORConfig{}, fmt.Errorf("在modoverrides.lua文件中没有找到该mod的配置")
}

func (g *Game) modEnable(worldID, modID int, ugc bool) error {
	var (
		err     error
		options *[]ConfigurationOption
	)
	// 区分是否为禁本地配置
	if modID == 0 {
		options = &[]ConfigurationOption{}
	} else {
		options, err = g.getModConfigureOptions(worldID, modID, ugc)
		if err != nil {
			logger.Logger.Debugf("这里出问题?, err: %v", err)
			return err
		}
	}

	newModConfig := &ModORConfig{
		ConfigurationOptions: make(map[string]any),
		Enabled:              true,
	}
	for _, option := range *options {
		key := option.Name
		value := option.Default
		newModConfig.ConfigurationOptions[key] = value
	}

	modORParser := NewModORParser()
	defer modORParser.close()

	var modORContent string
	if g.room.ModInOne {
		modORContent = g.room.ModData
		mods := make(ModORCollection)
		if modORContent != "" {
			mods, err = modORParser.Parse(modORContent, g.lang)
			if err != nil {
				logger.Logger.Debugf("这里出问题?, err: %v", err)
				return err
			}
		}
		// 区分是否为禁本地配置
		if modID == 0 {
			mods.AddModConfig(fmt.Sprintf("client_mods_disabled"), newModConfig)
		} else {
			mods.AddModConfig(fmt.Sprintf("workshop-%d", modID), newModConfig)
		}
		newModORContent := mods.ToLuaCode()
		g.room.ModData = newModORContent
	} else {
		// 为保留每个世界的独立模组配置，需要分开处理，增加指定的mod，并修改db，最后返回
		worlds := *g.worlds
		for i, world := range g.worldSaveData {
			modORContent = world.ModData
			mods := make(ModORCollection)
			if modORContent != "" {
				mods, err = modORParser.Parse(modORContent, g.lang)
				if err != nil {
					logger.Logger.Debugf("这里出问题?, err: %v", err)
					return err
				}
			}

			// 区分是否为禁本地配置
			if modID == 0 {
				mods.AddModConfig(fmt.Sprintf("client_mods_disabled"), newModConfig)
			} else {
				mods.AddModConfig(fmt.Sprintf("workshop-%d", modID), newModConfig)
			}
			newModORContent := mods.ToLuaCode()

			worlds[i].ModData = newModORContent
		}
	}

	// 统一保存文件
	return g.saveMods()
}

func (g *Game) saveMods() error {
	var modContent string

	for idx, world := range *g.worlds {
		if g.room.ModInOne {
			modContent = g.room.ModData
		} else {
			modContent = world.ModData
		}
		err := utils.TruncAndWriteFile(g.worldSaveData[idx].modOverridesPath, modContent)
		if err != nil {
			return err
		}
	}

	return nil
}

func (g *Game) modConfigureOptionsValuesChange(worldID, modID int, modConfig *ModORConfig) error {
	g.modMutex.Lock()
	defer g.modMutex.Unlock()

	modORParser := NewModORParser()
	defer modORParser.close()

	var modORContent string
	if g.room.ModInOne {
		modORContent = g.room.ModData
	} else {
		world, err := g.getWorldByID(worldID)
		if err != nil {
			logger.Logger.Debugf("这里出问题?, err: %v", err)
			return err
		}
		modORContent = world.ModData
	}

	mods, err := modORParser.Parse(modORContent, g.lang)
	if err != nil {
		logger.Logger.Debugf("这里出问题?, err: %v", err)
		return err
	}

	modKey := fmt.Sprintf("workshop-%d", modID)

	mods[modKey] = modConfig

	newModORContent := mods.ToLuaCode()

	if g.room.ModInOne {
		g.room.ModData = newModORContent
	} else {
		for i := range g.worldSaveData {
			worlds := *g.worlds
			if worlds[i].ID == worldID {
				worlds[i].ModData = newModORContent
			}
		}
	}

	return g.saveMods()
}

func (g *Game) getEnabledMods(worldID int) ([]DownloadedMod, error) {
	modORParser := NewModORParser()
	defer modORParser.close()

	var modORContent string
	if g.room.ModInOne {
		modORContent = g.room.ModData
	} else {
		world, err := g.getWorldByID(worldID)
		if err != nil {
			logger.Logger.Debugf("这里出问题?, err: %v", err)
			return []DownloadedMod{}, err
		}
		modORContent = world.ModData
	}

	if modORContent == "" {
		return []DownloadedMod{}, nil
	}

	mods, err := modORParser.Parse(modORContent, g.lang)
	if err != nil {
		logger.Logger.Warnf("可能存在模组配置注入，已拦截: %v", err)
		return []DownloadedMod{}, err
	}

	var modsID []DownloadedMod
	for k := range mods {
		modIDSlice := strings.Split(k, "-")
		var modID int
		if len(modIDSlice) < 2 {
			// 禁本地配置
			modID = 0
		} else {
			modID, err = strconv.Atoi(modIDSlice[1])
			if err != nil {
				modID = 0
			}
		}
		modsID = append(modsID, DownloadedMod{
			ID: modID,
		})
	}

	return modsID, nil
}

func (g *Game) modDisable(modID int) error {
	modORParser := NewModORParser()
	defer modORParser.close()

	var modORContent string
	if g.room.ModInOne {
		modORContent = g.room.ModData
		mods, err := modORParser.Parse(modORContent, g.lang)
		if err != nil {
			logger.Logger.Debugf("这里出问题?, err: %v", err)
			return err
		}
		// 区分是否为禁本地配置
		if modID == 0 {
			delete(mods, fmt.Sprintf("client_mods_disabled"))
		} else {
			delete(mods, fmt.Sprintf("workshop-%d", modID))
		}

		newModORContent := mods.ToLuaCode()

		g.room.ModData = newModORContent
	} else {
		// 为保留每个世界的独立模组配置，需要分开处理，删除指定的mod，并修改db，最后返回
		worlds := *g.worlds
		for i, world := range g.worldSaveData {
			modORContent = world.ModData
			mods, err := modORParser.Parse(modORContent, g.lang)
			if err != nil {
				logger.Logger.Debugf("这里出问题?, err: %v", err)
				return err
			}

			// 区分是否为禁本地配置
			if modID == 0 {
				delete(mods, fmt.Sprintf("client_mods_disabled"))
			} else {
				delete(mods, fmt.Sprintf("workshop-%d", modID))
			}

			newModORContent := mods.ToLuaCode()

			worlds[i].ModData = newModORContent
		}
	}

	return g.saveMods()
}

type DeleteModInfo struct {
	ID      int    `json:"id"`
	FileUrl string `json:"file_url"`
}

func (g *Game) deleteMod(modID int, fileURL string) error {
	var ugc bool

	if fileURL == "" {
		ugc = true
	}

	if ugc {
		g.acfMutex.Lock()
		defer g.acfMutex.Unlock()

		acfID := strconv.Itoa(modID)

		for _, world := range g.worldSaveData {
			gameAcfPath := fmt.Sprintf("%s/%s/appworkshop_322330.acf", g.ugcPath, world.WorldName)

			err := utils.EnsureFileExists(gameAcfPath)
			if err != nil {
				logger.Logger.Errorf("acf文件不存在, path: %v", gameAcfPath)
				return err
			}

			gameAcfParser, err := utils.NewParser(gameAcfPath)
			if err != nil {
				return err
			}

			err = gameAcfParser.RemoveWorkshopItemsInstalled(acfID)
			if err != nil {
				return err
			}

			writtenContent := strings.Join(gameAcfParser.Format(), "\n")
			err = utils.TruncAndWriteFile(gameAcfPath, writtenContent)
			if err != nil {
				return err
			}

			modPath := fmt.Sprintf("%s/%s/content/322330/%d", g.ugcPath, world.WorldName, modID)
			err = utils.RemoveDir(modPath)
			if err != nil {
				logger.Logger.Errorf("删除模组失败, err: %v", err)
				return err
			}
		}
	} else {
		err := utils.RemoveDir(fmt.Sprintf("%s/workshop-%d", g.notUgcPath, modID))
		if err != nil {
			logger.Logger.Errorf("删除模组失败, err: %v", err)
			return err
		}
	}

	return nil
}

func (g *Game) deleteAcf() error {
	for _, world := range g.worldSaveData {
		gameAcfPath := fmt.Sprintf("%s/%s/appworkshop_322330.acf", g.ugcPath, world.WorldName)
		err := utils.RemoveFile(gameAcfPath)
		if err != nil {
			return err
		}
	}

	dmpModDownloadAcfPath := fmt.Sprintf("%s/mods/ugc/%s/steamapps/workshop/appworkshop_322330.acf", constant.DmpFiles, g.clusterName)
	_ = utils.RemoveFile(dmpModDownloadAcfPath)

	return nil
}
