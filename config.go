package main

// FinallyMigrate 自己的小配置（区别于 Finallyshell 的 hosts.json）。
// 目前只记主窗口的偏好：是否隐藏已删除（回收站）的连接。
//
// 文件：%APPDATA%\FinallyMigrate\config.json；读不到或损坏时回退默认值，
// 不影响任何功能。

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// config 是 FinallyMigrate 的用户偏好。零值不是有效默认，统一走 defaultConfig。
type config struct {
	HideDeleted bool `json:"hideDeleted"` // 主窗口隐藏已删除(回收站)的连接
}

// defaultConfig 是首次使用时的偏好：列表干净优先，已删除的默认不显示。
func defaultConfig() config {
	return config{HideDeleted: true}
}

// configPath 是 FinallyMigrate 配置文件的位置：os.UserConfigDir()/FinallyMigrate/config.json。
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "FinallyMigrate", "config.json"), nil
}

// loadConfig 读配置；文件不存在、损坏或目录读不出时回退默认值（不报错，
// 配置只影响偏好，不该挡住功能）。
func loadConfig() config {
	cfg := defaultConfig()
	p, err := configPath()
	if err != nil {
		return cfg
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig()
	}
	return cfg
}

// saveConfig 原子写配置（写临时文件后替换）；失败时返回错误，由调用方
// 决定如何提示（状态栏）。
func saveConfig(cfg config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
