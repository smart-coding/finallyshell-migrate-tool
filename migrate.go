package main

// FinalShell → Finallyshell 密码补写：
//
// 连接信息由 Finallyshell 自带的导入功能搬进去（官方导入器不搬密码），
// 本工具负责补上缺的密码：
//
//  1. 读 Finallyshell 的 hosts.json，得到它现有的主机；
//  2. 递归读 FinalShell 的 conn 目录（子目录即分组），用 fstool.go 的
//     算法解密连接里存的密码；
//  3. 按 addr+port+user 逐条比对：匹配到且还没存密码的主机，把解密出
//     的密码按 Finallyshell 的读取方式写进系统凭据管理器（service
//     "finallyshell"，键 host/<id>/password）。
//
// 全程只写凭据管理器，不改 hosts.json，所以 Finallyshell 运行中也可以
// 补写；已存密码的主机绝不覆盖。
//
// 用法：先在 Finallyshell 的连接管理器里用它自带的导入功能把 FinalShell
// 连接导入（只缺密码），再运行本工具补写密码，补完即可直接连接。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// fsConn 是 FinalShell 连接文件里我们读的字段。
type fsConn struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       fsInt  `json:"port"`
	UserName   string `json:"user_name"`
	ConnType   fsInt  `json:"conection_type"` // 100 = SSH
	AuthType   fsInt  `json:"authentication_type"` // 1 = 密码, 2 = 公钥
	Password   string `json:"password"`
	ParentID   string `json:"parent_id"`
	Descr      string `json:"description"`
	DeleteTime int64  `json:"delete_time"`
}

// fsInt 兼容数字和字符串两种 JSON 形态（FinalShell 各版本不一）。
type fsInt int

func (p *fsInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*p = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("端口不是数字: %s", s)
	}
	*p = fsInt(n)
	return nil
}

const (
	fsConnTypeSSH = 100
	fsAuthPass    = 1
)

// findFinalShellDir 依次探测 FinalShell 数据目录，返回第一个含 conn 的。
func findFinalShellDir() string {
	var cands []string
	add := func(parts ...string) {
		if parts[0] != "" {
			cands = append(cands, filepath.Join(parts...))
		}
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		local = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}
	add(local, "finalshell")
	add(os.Getenv("APPDATA"), "finalshell")
	add(os.Getenv("ProgramFiles"), "FinalShell")
	add(os.Getenv("ProgramFiles(x86)"), "FinalShell")
	if drive := os.Getenv("SystemDrive"); drive != "" {
		add(strings.TrimRight(drive, `\`), "finalshell")
	}
	// 绿色版常见位置（用户的安装就在其中之一）
	for _, d := range []string{"C", "D", "E", "F"} {
		add(d+`:`, "programFile", "finalshell")
		add(d+`:`, "finalshell")
	}
	for _, dir := range cands {
		if st, err := os.Stat(filepath.Join(dir, "conn")); err == nil && st.IsDir() {
			return dir
		}
	}
	return ""
}

// planItem 是一条连接的补写判定。
type planItem struct {
	Name     string // 连接名
	Folder   string // FinalShell 里的相对目录（"组A/组B"），"" 为顶层
	Addr     string // host:port
	User     string
	Action   string // fill / has-pw / no-match / skip
	Note     string // 匹配结果、跳过原因等
	Password string // 解密后的密码（仅内存，不打印不日志）
	DecErr   error  // 解密失败
	HostID   string // 匹配到的 Finallyshell 主机 ID（fill 条目）
	Selected bool   // GUI 勾选：是否补写这条（只有 fill 条目才有意义）
}

// importable 说这条是否可补写（GUI 里才提供勾选）。
func (it *planItem) importable() bool {
	return it.Action == actionFill
}

const (
	actionFill    = "fill"     // 匹配到主机且缺密码：补写
	actionHasPw   = "has-pw"   // 匹配到主机但已存密码：不覆盖
	actionNoMatch = "no-match" // Finallyshell 里没有这台主机
	actionSkip    = "skip"     // 已删除/非 SSH/解密失败等
)

// 扫描保护上限（与 Finallyshell 官方导入器一致）：分组目录就是 conn 下的
// 真实子目录，连接文件按目录层级递归收集，防呆防脏数据。
const (
	maxWalkDepth   = 16      // 分组最多嵌套层数
	maxConnFiles   = 10000   // 连接文件总数上限
	maxConnFileLen = 1 << 20 // 单个连接文件 1 MiB
)

// portOr22 归一化端口：Finallyshell 存主机时对 22 端口省略 port 字段
// （omitempty），读回来是 0；FinalShell 那边端口缺失也按 22 处理。
func portOr22(p int) int {
	if p == 0 {
		return 22
	}
	return p
}

// fsFolderRec 是 FinalShell 的一份分组记录。真实数据目录里磁盘目录名是
// 分组的 ID（编码形态），真实名称在子目录的 folder.json 里（id/name/
// parent_id/delete_time）；没有 folder.json 的目录（FinalShell 导出）才
// 用目录名当分组名。嵌套关系靠 parent_id（"root"/"" 为顶层）。
type fsFolderRec struct {
	id      string
	name    string
	parent  string
	deleted bool
}

// topOrFs 解读 parent_id："" 表示沿用所在目录的分组，"root" 表示顶层。
func topOrFs(parent, outer string) string {
	switch parent {
	case "":
		return outer
	case "root":
		return ""
	}
	return parent
}

// fsFolderTable 收集扫描中遇到的分组：按 ID 索引 + 每个目录对应的分组 ID。
type fsFolderTable struct {
	byID map[string]*fsFolderRec
	byDir map[string]string // 相对路径（/分隔）→ 分组 ID，顶层 "." 不登记
}

func newFsFolderTable() *fsFolderTable {
	return &fsFolderTable{byID: map[string]*fsFolderRec{}, byDir: map[string]string{}}
}

// addDir 登记一个子目录的分组：优先 folder.json，否则目录名即分组名。
// rel 是相对 conn 的路径（OS 分隔符）。
func (t *fsFolderTable) addDir(connDir, path, rel string) {
	relS := filepath.ToSlash(rel)
	id := "dir:" + relS
	rec := &fsFolderRec{id: id, name: filepath.Base(relS), parent: t.byDir[parentDir(relS)]}
	if data, err := os.ReadFile(filepath.Join(path, "folder.json")); err == nil {
		var vj struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			ParentID   string `json:"parent_id"`
			DeleteTime int64  `json:"delete_time"`
		}
		if json.Unmarshal(data, &vj) == nil {
			if vj.ID != "" {
				rec.id = vj.ID
			}
			if strings.TrimSpace(vj.Name) != "" {
				rec.name = strings.TrimSpace(vj.Name)
			}
			rec.parent = topOrFs(vj.ParentID, rec.parent)
			rec.deleted = vj.DeleteTime != 0
		}
	}
	if t.byID[rec.id] == nil {
		t.byID[rec.id] = rec
	}
	t.byDir[relS] = rec.id
}

// parentDir 是相对路径的父目录（"." 的父还是 "."，查表得到顶层空 ID）。
func parentDir(relS string) string {
	return filepath.ToSlash(filepath.Dir(relS))
}

// relSlash 是 path 相对 base 的路径（/ 分隔）；失败时按原路径处理。
func relSlash(base, path string) string {
	if rel, err := filepath.Rel(base, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// path 解出连接归属分组的显示路径（顶层在前，如 "生产环境/数据库"）：
// 连接的 parent_id 指向分组 ID，未知时回退到所在目录的分组；删除掉的、
// 名字为空的分组不进路径。
func (t *fsFolderTable) path(parentID, dirID string) string {
	id := topOrFs(parentID, dirID)
	if id != "" && t.byID[id] == nil {
		id = dirID // 未知 ID：回退到所在目录的分组
	}
	var names []string
	seen := map[string]bool{}
	for rec, ok := t.byID[id]; ok && !seen[rec.id]; rec, ok = t.byID[rec.parent] {
		seen[rec.id] = true
		if !rec.deleted && strings.TrimSpace(rec.name) != "" {
			names = append(names, strings.TrimSpace(rec.name))
		}
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return strings.Join(names, "/")
}

// actionCounts 是各判定的条数，供扫描摘要展示。
func actionCounts(items []planItem) map[string]int {
	c := map[string]int{}
	for _, it := range items {
		c[it.Action]++
	}
	return c
}

// planMigration 读 Finallyshell 现有主机，扫描 FinalShell 并逐条比对。
func planMigration(fsDir string) ([]planItem, error) {
	hosts, err := loadHosts()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return planAgainst(hosts, fsDir)
}

// planAgainst 递归扫描 FinalShell，与给定主机列表逐条比对并解密密码
// （只读不写）。与 loadHosts 解耦是为了可测。
//
// 真实数据目录里磁盘目录名是分组的编码 ID，真实分组名在各子目录的
// folder.json 里；目录层级 + parent_id 共同决定归属（parent_id 优先）。
func planAgainst(hosts []host, fsDir string) ([]planItem, error) {
	connDir := filepath.Join(fsDir, "conn")
	var files []string
	table := newFsFolderTable()
	err := filepath.WalkDir(connDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if path == connDir {
				return err // conn 本身进不去才是硬错误
			}
			return nil // 某个子目录读不了：跳过，不中断整个扫描
		}
		rel, rerr := filepath.Rel(connDir, path)
		if rerr != nil {
			return nil
		}
		relS := filepath.ToSlash(rel)
		if d.IsDir() {
			if path == connDir {
				return nil // conn 根目录本身不是分组
			}
			if strings.Count(relS, "/") >= maxWalkDepth {
				return filepath.SkipDir
			}
			table.addDir(connDir, path, rel) // WalkDir 先父后子，父目录已登记
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), "_connect_config.json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", connDir, err)
	}
	if len(files) > maxConnFiles {
		return nil, fmt.Errorf("连接文件超过 %d 个（%s 下），疑似选错目录", maxConnFiles, connDir)
	}

	var out []planItem
	for _, path := range files {
		name := filepath.Base(path)
		// 连接所在目录（相对 conn）对应的分组；顶层 "." 查不到 → ""
		dirID := table.byDir[relSlash(connDir, filepath.Dir(path))]
		st, serr := os.Stat(path)
		if serr == nil && st.Size() > maxConnFileLen {
			out = append(out, planItem{Name: name, Folder: table.path("", dirID), Action: actionSkip, Note: "文件过大（>1MiB），疑似损坏"})
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			out = append(out, planItem{Name: name, Folder: table.path("", dirID), Action: actionSkip, Note: "读文件失败: " + err.Error()})
			continue
		}
		var fc fsConn
		if err := json.Unmarshal(data, &fc); err != nil {
			out = append(out, planItem{Name: name, Folder: table.path("", dirID), Action: actionSkip, Note: "解析失败: " + err.Error()})
			continue
		}
		it := planItem{Name: fc.Name, Folder: table.path(fc.ParentID, dirID), User: fc.UserName,
			Addr: fmt.Sprintf("%s:%d", fc.Host, portOr22(int(fc.Port)))}
		switch {
		case fc.DeleteTime != 0:
			it.Action, it.Note = actionSkip, "FinalShell 里已删除"
		case fc.ConnType != fsConnTypeSSH:
			it.Action, it.Note = actionSkip, fmt.Sprintf("不是 SSH 连接（类型 %d），跳过", fc.ConnType)
		case fc.AuthType != fsAuthPass:
			it.Action, it.Note = actionSkip, "不是密码认证（密钥主机没有存密码可补）"
		case fc.Host == "":
			it.Action, it.Note = actionSkip, "没有主机地址"
		default:
			// 先比对再解密：未匹配/已存密码的主机不用碰密码密文
			dup := findDup(hosts, fc.Host, int(fc.Port), fc.UserName)
			switch {
			case dup == nil:
				it.Action, it.Note = actionNoMatch, "Finallyshell 里没有这台主机，请先用它的连接管理器导入"
			case dup.Deleted():
				it.Action, it.Note = actionSkip, "匹配到的是回收站里的主机（"+dup.Name+"）"
			case hasCred(dup):
				it.Action, it.Note = actionHasPw, "已存密码，不覆盖"
			default:
				// 解密（严格模式：去填充失败视为解密失败）
				pw, derr := fsDecodePass(fc.Password)
				if derr != nil && fc.Password != "" {
					it.Action, it.DecErr = actionSkip, derr
					it.Note = "密码解密失败: " + derr.Error()
					out = append(out, it)
					continue
				}
				if pw == "" {
					it.Action, it.Note = actionSkip, "FinalShell 里没存密码，连接时再输"
					break
				}
				it.Password = pw
				it.Action = actionFill
				it.HostID = dup.ID
				it.Note = "匹配到主机 " + dup.Name
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Folder != out[j].Folder {
			return out[i].Folder < out[j].Folder
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// findDup 按 addr+port+user 找已有主机（大小写不敏感），优先未删除的；
// 只有回收站里有时返回那个。端口按 22 归一化比较：Finallyshell 存主机时
// 对 22 端口省略 port 字段，读回来是 0。
func findDup(hosts []host, addr string, port int, user string) *host {
	port = portOr22(port)
	var trashed *host
	for i := range hosts {
		h := &hosts[i]
		if strings.EqualFold(h.Addr, addr) && portOr22(h.Port) == port &&
			strings.EqualFold(h.User, user) {
			if !h.Deleted() {
				return h
			}
			if trashed == nil {
				trashed = h
			}
		}
	}
	return trashed
}

// hasCred 说 Finallyshell 是否已为这台主机存过登录凭据（密码或私钥口令）。
func hasCred(h *host) bool {
	for _, k := range []string{"host/" + h.ID + "/password", "host/" + h.ID + "/passphrase"} {
		if _, err := secrets.Get(k); err == nil {
			return true
		}
	}
	return false
}

// migrateResult 是补写的执行结果。
type migrateResult struct {
	Filled    []string // 补写了密码的主机（名称 (addr)）
	SecretsOK int      // 成功写入的密码数
	SecretErr []string
}

// fillPasswords 执行补写：把条目的密码写进系统凭据管理器。只写凭据、
// 不碰 hosts.json，Finallyshell 运行中也可以执行（它连接时才读凭据）。
func fillPasswords(items []planItem) (*migrateResult, error) {
	res := &migrateResult{}
	for _, it := range items {
		if it.Action != actionFill {
			continue
		}
		if err := secrets.Set("host/"+it.HostID+"/password", []byte(it.Password)); err != nil {
			res.SecretErr = append(res.SecretErr, fmt.Sprintf("%s: %v", it.Name, err))
		} else {
			res.SecretsOK++
			res.Filled = append(res.Filled, fmt.Sprintf("%s (%s)", it.Name, it.Addr))
		}
	}
	return res, nil
}

// runMigration 是 CLI 入口：扫描出完整计划后全量补写。
func runMigration(fsDir string) (*migrateResult, error) {
	items, err := planMigration(fsDir)
	if err != nil {
		return nil, err
	}
	return fillPasswords(items)
}

// cmdMigrate 是 migrate-finalshell 子命令：先出比对计划，-y 或回答 y 执行。
func cmdMigrate(args []string) error {
	yes := false
	var dir string
	for _, a := range args {
		if a == "-y" {
			yes = true
			continue
		}
		dir = a
	}
	if dir == "" {
		dir = findFinalShellDir()
		if dir == "" {
			return errors.New("没有自动找到 FinalShell 数据目录，请把目录作为参数传入（含 conn 子目录）")
		}
	}
	fmt.Println("FinalShell 目录:", dir)
	items, err := planMigration(dir)
	if err != nil {
		return err
	}
	fmt.Printf("扫描到 %d 条连接:\n", len(items))
	fill := 0
	for _, it := range items {
		mark := map[string]string{
			actionFill:    "[补密码]",
			actionHasPw:   "[不覆盖]",
			actionNoMatch: "[未匹配]",
			actionSkip:    "[跳过]  ",
		}[it.Action]
		line := fmt.Sprintf("  %s %-20s %-14s %-18s %s", mark, it.Name, it.Folder, it.User+"@"+it.Addr, actionLabel(it))
		fmt.Println(line)
		if it.Action == actionFill {
			fill++
		}
	}
	c := actionCounts(items)
	fmt.Printf("汇总：共 %d 条，可补写 %d，已存密码不覆盖 %d，未找到主机 %d，其他跳过 %d。\n",
		len(items), c[actionFill], c[actionHasPw], c[actionNoMatch], c[actionSkip])
	if fill == 0 {
		fmt.Println("没有可补写的条目。")
		return nil
	}
	if !yes {
		fmt.Print("执行补写？(y/N): ")
		var ans string
		fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			fmt.Println("已取消。")
			return nil
		}
	}
	res, err := runMigration(dir)
	if err != nil {
		return err
	}
	fmt.Printf("\n完成：补写密码 %d 条。\n", res.SecretsOK)
	for _, s := range res.SecretErr {
		fmt.Println("密码写入失败:", s)
	}
	return nil
}
