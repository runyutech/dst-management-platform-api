package dst

import (
	"os"
	"path/filepath"
	"strings"

	"dst-management-platform-api/cache"
	"dst-management-platform-api/logger"
)

// SteamCellID 固定 SteamCMD 的内容服务器区域。
//
// 国内机器按 IP 探测经常被分到延迟高或不稳定的 CDN，表现是 manifest 拉不下来、
// 工坊模组一直卡在 0%。config.vdf 里的 CellIDServerOverride 可以指定区域，45 是香港 cell。
// 安装时由控制端模板写一次，但 steamcmd 退出会整份重写 config.vdf，登录后也可能按探测值重置，
// 所以每次跑 steamcmd 之前都再纠正一次。
const SteamCellID = "45"

// steamConfigSkeleton 是 config.vdf 的最小骨架，只在文件损坏或结构里找不到插入点时用
const steamConfigSkeleton = `"InstallConfigStore"
{
	"Software"
	{
		"Valve"
		{
			"Steam"
			{
				"CellIDServerOverride"		"` + SteamCellID + `"
			}
		}
	}
}
`

// EnsureSteamCellID 保证 steamcmd/config/config.vdf 里的 CellIDServerOverride 是 SteamCellID。
// 写失败只打日志，不影响安装、更新和模组下载。
func EnsureSteamCellID() {
	path := filepath.Join(cache.CurrentDir, "steamcmd", "config", "config.vdf")

	content, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Logger.Errorf("读取 Steam 配置失败: %v", err)
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			logger.Logger.Errorf("创建 Steam 配置目录失败: %v", err)
			return
		}
		if err := os.WriteFile(path, []byte(steamConfigSkeleton), 0o644); err != nil {
			logger.Logger.Errorf("写入 Steam 配置失败: %v", err)
		}
		return
	}

	text := string(content)
	if text == steamConfigSkeleton {
		return
	}
	updated, ok := setCellID(text)
	if !ok {
		logger.Logger.Warnf("Steam 配置里没有可插入 CellID 的位置，改用默认骨架")
		updated = steamConfigSkeleton
	}
	if updated == text {
		return
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		logger.Logger.Errorf("更新 Steam 配置失败: %v", err)
	}
}

// setCellID 把已有的 CellIDServerOverride 改成 SteamCellID，没有就插到 "Steam" 段里。
// 第二个返回值为 false 表示结构里找不到 "Steam" 段。
func setCellID(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if !strings.Contains(line, `"CellIDServerOverride"`) {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = indent + `"CellIDServerOverride"` + "\t\t" + `"` + SteamCellID + `"`
		return strings.Join(lines, "\n"), true
	}

	for i, line := range lines {
		if strings.TrimSpace(line) != `"Steam"` {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if !strings.Contains(lines[j], "{") {
				continue
			}
			indent := lines[j][:len(lines[j])-len(strings.TrimLeft(lines[j], " \t"))] + "\t"
			inserted := make([]string, 0, len(lines)+1)
			inserted = append(inserted, lines[:j+1]...)
			inserted = append(inserted, indent+`"CellIDServerOverride"`+"\t\t"+`"`+SteamCellID+`"`)
			inserted = append(inserted, lines[j+1:]...)
			return strings.Join(inserted, "\n"), true
		}
		return content, false
	}
	return content, false
}
