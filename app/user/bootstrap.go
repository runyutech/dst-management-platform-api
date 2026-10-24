package user

import (
	"dst-management-platform-api/constant"
	"dst-management-platform-api/database/dao"
	"dst-management-platform-api/database/models"
	"dst-management-platform-api/logger"
	"dst-management-platform-api/utils"
	"os"
	"strings"
)

// EnsureEnvAdmin 按容器传入的环境变量准备管理员账号。
//
// RGS 的启动命令会把面板变量导成 DMP_ADMIN_PASSWORD，这样买完机器不用再走一次
// 「注册」就能直接用带密码的链接登录。没配密码时什么都不做，普通部署行为不变
// （第一个访问的人仍然走注册页成为管理员）。
//
// 环境变量是这份密码的唯一来源：账号已存在但密码不一致时会被重置，
// 这样详情页那条一键登录链接始终有效。在面板里改的密码重启后会回到变量里的值。
func EnsureEnvAdmin(userDao *dao.UserDAO) {
	password := strings.TrimSpace(os.Getenv(constant.EnvAdminPassword))
	if password == "" {
		logger.Logger.Infof("未设置 %s，跳过自动创建管理员；首次访问仍需在注册页建号", constant.EnvAdminPassword)
		return
	}
	if len(password) < constant.EnvAdminPasswordMinLength || len(password) > constant.EnvAdminPasswordMaxLength {
		logger.Logger.Errorf("环境变量 %s 长度不合法（%d 位），应为 %d-%d 位；本次不创建/重置管理员",
			constant.EnvAdminPassword, len(password), constant.EnvAdminPasswordMinLength, constant.EnvAdminPasswordMaxLength)
		return
	}

	username := strings.TrimSpace(os.Getenv(constant.EnvAdminUsername))
	if username == "" {
		username = constant.DefaultAdminUsername
	}

	total, err := userDao.Count(nil)
	if err != nil {
		logger.Logger.Errorf("查询用户失败，跳过自动创建管理员, err: %v", err)
		return
	}

	dbUser, err := userDao.GetUserByUsername(username)
	if err != nil {
		logger.Logger.Errorf("查询管理员 %s 失败，跳过自动创建, err: %v", username, err)
		return
	}

	if dbUser.Username != "" {
		if dbUser.PasswordVersion == models.PasswordVersionBcrypt && utils.ValidatePassword(password, dbUser.Password) {
			logger.Logger.Infof("管理员 %s 已存在，密码与环境变量一致", username)
			return
		}
		hashed, err := utils.GenerateBcryptPassword(password)
		if err != nil {
			logger.Logger.Errorf("创建bcrypt密码失败：%v", err)
			return
		}
		if err := userDao.UpdatePassword(username, hashed, models.PasswordVersionBcrypt); err != nil {
			logger.Logger.Errorf("重置管理员 %s 密码失败, err: %v", username, err)
			return
		}
		logger.Logger.Infof("已按 %s 重置管理员 %s 的面板密码", constant.EnvAdminPassword, username)
		return
	}

	// 面板里已经有手工注册的账号，但不是这个名字：不去动别人的账号，只提示一句
	if total > 0 {
		logger.Logger.Warnf("面板里已有 %d 个手工注册的账号，没有 %s；%s 本次不生效。"+
			"想用一键登录需要把某个账号改成 %s（或删掉后重启）", total, username, constant.EnvAdminPassword, username)
		return
	}

	hashed, err := utils.GenerateBcryptPassword(password)
	if err != nil {
		logger.Logger.Errorf("创建bcrypt密码失败：%v", err)
		return
	}
	admin := models.User{
		Username:        username,
		Nickname:        username,
		Role:            "admin",
		Avatar:          "1",
		Password:        hashed,
		PasswordVersion: models.PasswordVersionBcrypt,
	}
	if err := userDao.Create(&admin); err != nil {
		logger.Logger.Errorf("创建管理员 %s 失败, err: %v", username, err)
		return
	}
	logger.Logger.Infof("已按 %s 创建管理员 %s", constant.EnvAdminPassword, username)
}
