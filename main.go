// FinallyMigrate —— Finallyshell 凭据迁移工具：查看/找回 Finallyshell
// 存在 Windows 凭据管理器里的密码，并从 FinalShell 补写迁移密码。
//
// 与 Finallyshell 共享同一存储：service 固定为 "finallyshell"，键为
// "host/<id>/password"（或 passphrase、proxy-password），与源码
// keychain.go 一致。同一 Windows 账户运行即可读回原文，无需确认。
//
// 用法:
//   FinallyMigrate list           列出 hosts.json 里的主机和各自已存的凭据
//   FinallyMigrate read <key>     读取一条凭据原文，如 host/h123…/password
//   FinallyMigrate read <关键字>   按名称/地址子串找到主机并读它的登录密码
//                                 （key 认证的主机读 passphrase）
//   FinallyMigrate set <key> <值> 写入（测试用）
//   FinallyMigrate del <key>      删除（清理用）
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo"
)

const service = "finallyshell"

var secrets = mygo.Secrets.Service(service)

// host 是 hosts.json 里我们需要的字段。
type host struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Addr      string    `json:"addr"`
	Port      int       `json:"port"`
	User      string    `json:"user"`
	Auth      string    `json:"auth"`
	Folder    string    `json:"folder"`    // 归属文件夹 ID，"" 为顶层
	DeletedAt time.Time `json:"deletedAt"` // 非零 = 在 Finallyshell 的回收站里
}

// Deleted 说该主机是否被 Finallyshell 软删除（凭据往往还留在系统里）。
func (h host) Deleted() bool { return !h.DeletedAt.IsZero() }

// folder 是 hosts.json folders 数组里的分组；嵌套关系靠 Parent（父 ID，
// 顶层为空）。
type folder struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Parent string `json:"parent"`
}

// secretNames 是 Finallyshell 会为每台主机存的全部凭据种类（与源码
// hostSecretNames 一致）。删除全部凭据时按这些键清理。
var secretNames = []string{"password", "passphrase", "proxy-password"}

// hostsPath 是 Finallyshell 的 hosts.json：os.UserConfigDir()/finallyshell/hosts.json。
func hostsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "finallyshell", "hosts.json"), nil
}

func loadHosts() ([]host, error) {
	p, err := hostsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", p, err)
	}
	// hosts.json 的顶层可能是 {"hosts": [...]} 或直接数组（Store 结构兼容两者）。
	var wrap struct {
		Hosts []host `json:"hosts"`
	}
	if err := json.Unmarshal(data, &wrap); err == nil && wrap.Hosts != nil {
		return wrap.Hosts, nil
	}
	var list []host
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", p, err)
	}
	return list, nil
}

// loginKey 是主机的登录凭据键：key 认证是 passphrase，其余是 password。
// agent 认证没有凭据，返回 ""。
func loginKey(h host) string {
	switch h.Auth {
	case "key":
		return "host/" + h.ID + "/passphrase"
	case "agent":
		return ""
	default:
		return "host/" + h.ID + "/password"
	}
}

// loadFolders 读 hosts.json 顶层的 folders 数组（与 loadHosts 同一文件；
// v1 裸数组格式没有文件夹，返回空）。
func loadFolders() ([]folder, error) {
	p, err := hostsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", p, err)
	}
	var wrap struct {
		Folders []folder `json:"folders"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", p, err)
	}
	return wrap.Folders, nil
}

// folderPath 是文件夹的完整路径（顶层在前，如 "组A/组B"）。id 为空、
// 找不到时返回 ""；Parent 链上有环时每个文件夹只收一次，不挂死。
func folderPath(folders []folder, id string) string {
	if id == "" {
		return ""
	}
	byID := make(map[string]folder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}
	var names []string
	seen := map[string]bool{}
	for f, ok := byID[id]; ok && !seen[f.ID]; f, ok = byID[f.Parent] {
		seen[f.ID] = true
		names = append(names, f.Name)
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return strings.Join(names, "/")
}

// has 探测一条凭据是否已存（只看存在性，不取值）。
func has(key string) bool {
	_, err := secrets.Get(key)
	return err == nil
}

func label(h host) string {
	return fmt.Sprintf("%s\t%s@%s:%d", h.Name, h.User, h.Addr, h.Port)
}

func cmdList() error {
	hosts, err := loadHosts()
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		fmt.Println("hosts.json 里没有主机。")
		return nil
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Name+hosts[i].ID < hosts[j].Name+hosts[j].ID })
	fmt.Printf("%-24s %-28s %-9s %s\n", "名称", "地址", "认证", "已存的凭据")
	for _, h := range hosts {
		stored := "-"
		if k := loginKey(h); k != "" {
			switch {
			case has(k):
				stored = "✓ " + k
			case has("host/" + h.ID + "/password"), has("host/" + h.ID + "/passphrase"):
				stored = "✓ (另一种认证的凭据还在)"
			}
		}
		fmt.Printf("%-24s %-28s %-9s %s\n", h.Name, fmt.Sprintf("%s@%s:%d", h.User, h.Addr, h.Port), h.Auth, stored)
	}
	return nil
}

// findHosts 按名称或地址的子串找主机（大小写不敏感）。
func findHosts(hosts []host, q string) []host {
	q = strings.ToLower(q)
	var out []host
	for _, h := range hosts {
		if strings.Contains(strings.ToLower(h.Name), q) ||
			strings.Contains(strings.ToLower(h.Addr), q) ||
			strings.Contains(strings.ToLower(h.User), q) {
			out = append(out, h)
		}
	}
	return out
}

func cmdRead(arg string) error {
	if strings.HasPrefix(arg, "host/") {
		return readKey(arg)
	}
	hosts, err := loadHosts()
	if err != nil {
		return err
	}
	found := findHosts(hosts, arg)
	switch len(found) {
	case 0:
		return fmt.Errorf("hosts.json 里没有名称/地址含 %q 的主机", arg)
	case 1:
		h := found[0]
		k := loginKey(h)
		if k == "" {
			return fmt.Errorf("%s 用 SSH agent 认证，没有存密码", h.Name)
		}
		fmt.Fprintf(os.Stderr, "%s %s 的 %s:\n", h.Name, fmt.Sprintf("%s@%s:%d", h.User, h.Addr, h.Port), k)
		return readKey(k)
	default:
		fmt.Fprintf(os.Stderr, "匹配到 %d 台主机，请用更精确的关键字，或直接用键:\n", len(found))
		for _, h := range found {
			fmt.Fprintf(os.Stderr, "  %s  ->  %s\n", label(h), loginKey(h))
		}
		return errors.New("多台匹配")
	}
}

func readKey(key string) error {
	v, err := secrets.Get(key)
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return fmt.Errorf("凭据 %q 不存在（还没保存过）", key)
	}
	if err != nil {
		return err
	}
	fmt.Println(string(v))
	return nil
}

func cmdSet(key, value string) error {
	if err := secrets.Set(key, []byte(value)); err != nil {
		return err
	}
	fmt.Printf("已写入 %s/%s\n", service, key)
	return nil
}

func cmdDel(key string) error {
	if err := secrets.Delete(key); err != nil {
		return err
	}
	fmt.Printf("已删除 %s/%s\n", service, key)
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `FinallyMigrate —— Finallyshell 凭据迁移工具：查看凭据 / 从 FinalShell 补写密码

用法:
  FinallyMigrate               打开图形界面（列表 / 查看 / 修改 / 删除 / 补密码）
  FinallyMigrate gui           同上
  FinallyMigrate list          列出主机和各自已存的凭据（不显示密码内容）
  FinallyMigrate read <key>    读取凭据原文，key 形如 host/<id>/password
  FinallyMigrate read <关键字>  按名称/地址/用户子串找到主机，读它的登录密码
  FinallyMigrate set <key> <值> 写入一条凭据（测试用）
  FinallyMigrate del <key>     删除一条凭据
  FinallyMigrate migrate-finalshell [目录] [-y]
                               从 FinalShell 补写密码：先用 Finallyshell 自带
                               的导入功能导入连接（不含密码），再运行本命令
                               扫描 FinalShell 数据目录，给缺密码的主机补写。
                               目录默认自动探测；先显示比对计划，-y 直接执行。
`)
}

func main() {
	args := os.Args[1:]
	// 无参数（双击运行）或 gui：进图形界面。
	if len(args) == 0 || (len(args) == 1 && args[0] == "gui") {
		runGUI()
		return
	}
	var err error
	switch args[0] {
	case "list":
		err = cmdList()
	case "migrate-finalshell":
		err = cmdMigrate(args[1:])
	case "read":
		if len(args) != 2 {
			usage()
			os.Exit(2)
		}
		err = cmdRead(args[1])
	case "set":
		if len(args) != 3 {
			usage()
			os.Exit(2)
		}
		err = cmdSet(args[1], args[2])
	case "del":
		if len(args) != 2 {
			usage()
			os.Exit(2)
		}
		err = cmdDel(args[1])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
