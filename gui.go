// FinallyMigrate GUI —— Finallyshell 凭据迁移工具（图形版）
//
// 用 Finallyshell 同款的 mygo 框架画的窗口：左边主机列表，右边查看 /
// 修改 / 删除它存在 Windows 凭据管理器里的密码。存储读写与 Finallyshell
// 完全同路径：service "finallyshell"，键 "host/<id>/password|passphrase"。
//
// 运行: finallymigrate-gui.exe （或 go run . gui）
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// service、secrets、host、hostsPath、loadHosts、loginKey 复用 main.go 的。

// appIconPNG 是应用图标（assets/icon.png，256px，由 tools/genicon 生成，
// 编译期打包），窗口标题栏 / 任务栏图标用。exe 文件图标则由
// rsrc_windows_amd64.syso 资源嵌入。
//
//go:embed assets/icon.png
var appIconPNG []byte

// setIcon 给窗口设置图标；失败只影响观感，忽略。
func setIcon(win *mygo.Window) {
	_ = win.SetIcon(appIconPNG)
}

type app struct {
	win *mygo.Window

	cfg config // 用户偏好（隐藏已删除等），切换时写回配置文件

	hosts   []host
	folders []folder        // hosts.json 里的分组，供列表显示归属
	selID   string          // 选中的主机 ID, "" 无
	stored  map[string]bool // 已存在的凭据键
	loaded  bool            // 主机列表是否就绪
	query   string          // 搜索过滤：名称/地址/用户子串

	// showDeletedView 是“显示已删除(回收站)”勾选框的绑定状态，
	// 与 cfg.HideDeleted 互斥同步（ui.Checkbox 需要可写 *bool）。
	showDeletedView bool

	reveal  bool   // 详情里明文显示
	secret  string // 已读出的密码（选中主机）
	newVal  string // 新密码输入框
	status  string // 底部状态
	statusE bool   // 状态是否是错误

	imp    *importState   // 打开的导入窗口
	impWin *mygo.Window
}

// showDeleted 是“显示已删除”的当前生效状态。
func (a *app) showDeleted() bool { return a.showDeletedView }

// ---- 数据 ----

func (a *app) selected() (host, bool) {
	for _, h := range a.hosts {
		if h.ID == a.selID {
			return h, true
		}
	}
	return host{}, false
}

func (a *app) setStatus(s string, isErr bool) {
	a.status, a.statusE = s, isErr
}

// reload 读 hosts.json（主机 + 分组）并逐个探测凭据存在性（都在后台，
// 回 UI 线程改状态）。
func (a *app) reload() {
	go func() {
		hosts, err := loadHosts()
		if err != nil {
			a.post(func() { a.setStatus(err.Error(), true) })
			return
		}
		sort.Slice(hosts, func(i, j int) bool { return hosts[i].Name+hosts[i].ID < hosts[j].Name+hosts[j].ID })
		folders, ferr := loadFolders()
		a.post(func() {
			a.hosts, a.loaded = hosts, true
			if ferr == nil {
				a.folders = folders
			}
			if a.selID != "" && !a.hasID(a.selID) {
				a.selID, a.reveal, a.secret, a.newVal = "", false, "", ""
			}
		})
		for _, h := range hosts {
			for _, name := range secretNames {
				k := "host/" + h.ID + "/" + name
				_, gerr := secrets.Get(k)
				found := gerr == nil
				a.post(func() {
					if a.stored == nil {
						a.stored = map[string]bool{}
					}
					a.stored[k] = found
				})
			}
		}
	}()
}

func (a *app) hasID(id string) bool {
	for _, h := range a.hosts {
		if h.ID == id {
			return true
		}
	}
	return false
}

// post 在 UI 线程跑 fn 并重绘。
func (a *app) post(fn func()) {
	if a.win == nil {
		return
	}
	a.win.Update(fn)
}

// ---- 动作 ----

// hasAnySecret 说这台主机在系统里留有任何一种凭据记录。
func (a *app) hasAnySecret(h host) bool {
	for _, name := range secretNames {
		if a.stored["host/"+h.ID+"/"+name] {
			return true
		}
	}
	return false
}

// toggleShowDeleted 切换“显示已删除(回收站)连接”，偏好写回配置文件。
// 隐藏后若选中的是被过滤掉的已删除主机，自动关闭它的详情。
func (a *app) toggleShowDeleted() {
	a.cfg.HideDeleted = !a.showDeletedView
	if err := saveConfig(a.cfg); err != nil {
		a.setStatus("保存偏好失败: "+err.Error(), true)
	}
	if !a.showDeletedView {
		if h, ok := a.selected(); ok && h.Deleted() {
			a.selectHost("")
		}
	}
}

// folderText 是主机归属文件夹的显示文本（顶层在前的完整路径），"" 为顶层。
func (a *app) folderText(h host) string {
	return folderPath(a.folders, h.Folder)
}

// visibleHosts 按“隐藏已删除”偏好和搜索词过滤列表。
func (a *app) visibleHosts() []host {
	out := a.hosts
	if !a.showDeleted() {
		var keep []host
		for _, h := range out {
			if !h.Deleted() {
				keep = append(keep, h)
			}
		}
		out = keep
	}
	if strings.TrimSpace(a.query) != "" {
		out = findHosts(out, a.query)
	}
	return out
}

// hiddenDeletedCount 是被“隐藏已删除”偏好藏起来的已删除主机数。
func (a *app) hiddenDeletedCount() int {
	n := 0
	for _, h := range a.hosts {
		if h.Deleted() {
			n++
		}
	}
	return n
}

func (a *app) selectHost(id string) {
	a.post(func() {
		if a.selID == id {
			return
		}
		a.selID = id
		a.reveal, a.secret, a.newVal = false, "", ""
		a.setStatus("", false)
	})
}

// revealSecret 后台读密码，回 UI 明文显示。
func (a *app) revealSecret(h host, key string) {
	go func() {
		v, err := secrets.Get(key)
		a.post(func() {
			// 选中项可能已切换
			if cur, ok := a.selected(); !ok || cur.ID != h.ID {
				return
			}
			switch {
			case errors.Is(err, mygo.ErrSecretNotFound):
				a.setStatus("还没有保存过密码（勾选“记住密码”并连上一次后才会有）", true)
			case err != nil:
				a.setStatus("读取失败: "+err.Error(), true)
			default:
				a.secret, a.reveal = string(v), true
				a.setStatus("", false)
			}
		})
	}()
}

func (a *app) hideSecret() {
	a.post(func() { a.reveal = false })
}

// saveNew 把输入框里的新密码写进凭据管理器。
func (a *app) saveNew(h host, key string) {
	v := a.newVal
	if strings.TrimSpace(v) == "" {
		a.setStatus("新密码是空的", true)
		a.win.Invalidate()
		return
	}
	go func() {
		err := secrets.Set(key, []byte(v))
		a.post(func() {
			if err != nil {
				a.setStatus("保存失败: "+err.Error(), true)
				return
			}
			if a.stored == nil {
				a.stored = map[string]bool{}
			}
			a.stored[key] = true
			a.newVal, a.reveal, a.secret = "", false, ""
			a.setStatus("已保存到凭据管理器，下次连接生效（若与服务器真实密码不符将连不上）", false)
		})
	}()
}

// delStored 删除已存的登录密码（不动 hosts.json）。
func (a *app) delStored(h host, key string) {
	go func() {
		err := secrets.Delete(key)
		a.post(func() {
			if err != nil {
				a.setStatus("删除失败: "+err.Error(), true)
				return
			}
			if a.stored != nil {
				delete(a.stored, key)
			}
			a.reveal, a.secret = false, ""
			a.setStatus("已删除，下次连接 Finallyshell 会重新询问密码", false)
		})
	}()
}

// delAllSecrets 删除这台主机在系统凭据管理器里的全部记录：
// 登录密码、私钥口令、代理密码（含 proxy/<id> 老键名）。
func (a *app) delAllSecrets(h host) {
	go func() {
		keys := []string{
			"host/" + h.ID + "/password",
			"host/" + h.ID + "/passphrase",
			"host/" + h.ID + "/proxy-password",
			"proxy/" + h.ID, // 老键名，一并清理
		}
		removed := 0
		var existed []string
		for _, k := range keys {
			if _, err := secrets.Get(k); err == nil {
				existed = append(existed, k)
			}
			if err := secrets.Delete(k); err == nil {
				removed++
			}
		}
		a.post(func() {
			for _, k := range keys {
				delete(a.stored, k)
			}
			if a.selID == h.ID {
				a.reveal, a.secret = false, ""
			}
			a.setStatus(fmt.Sprintf("已删除 %d 条凭据记录（%s）。主机连接信息不受影响。", removed, strings.Join(existed, ", ")), false)
		})
	}()
}

// confirmDelAll 弹原生确认框，确认后删除全部凭据。
func (a *app) confirmDelAll(h host) {
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:  a.win,
			Type:    mygo.MessageQuestion,
			Title:   "删除全部凭据",
			Message: fmt.Sprintf("删除主机 %s（%s@%s:%d）在系统凭据管理器里的全部记录？", h.Name, h.User, h.Addr, h.Port),
			Detail:  "将删除它的登录密码、私钥口令、代理密码等所有凭据记录；连接信息（hosts.json）不受影响。此操作不可撤销。",
			Buttons: []string{"删除全部凭据", "取消"},
			// Windows 用第一个按钮当默认；明确取消键，删除键标为危险
			DefaultButton:      -1,
			CancelButton:       1,
			DestructiveButtons: []int{0},
		})
		if err != nil || res.Button != 0 {
			return // 取消或出错
		}
		a.post(func() { a.delAllSecrets(h) })
	}()
}

// ---- 视图 ----

func muted(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).TextColor(c.Theme().TextMuted).FontSize(12)
}

func (a *app) view(c *ui.Context) {
	ui.Column(c).Gap(10).Padding(14).Fill().Children(func() {
		// 标题行
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Finallyshell 凭据管理器").Bold().FontSize(16)
			muted(c, "（与 Finallyshell 共用 Windows 凭据管理器，service: "+service+"）")
			ui.Box(c).Grow(1) // 弹性空隙
			if ui.Button(c, "从 FinalShell 补密码").Clicked() {
				a.openImport()
			}
			if ui.Button(c, "刷新").Clicked() {
				a.reload()
			}
		})

		// 工具栏行：搜索 + 显示已删除开关 + 统计
		ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Box(c).FillWidth().Children(func() {
				ui.Field(c, "搜索名称/地址/用户", func() {
					ui.TextInput(c, &a.query)
				})
			})
			if ui.Checkbox(c, &a.showDeletedView, "显示已删除(回收站)").Changed() {
				a.toggleShowDeleted()
			}
			ui.Box(c).Grow(1)
			muted(c, a.statsText())
		})

		// 主机列表（按搜索词与“隐藏已删除”偏好过滤）
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).Gap(2).FillWidth().Children(func() {
				visible := a.visibleHosts()
				switch {
				case !a.loaded:
					muted(c, "读取 hosts.json …")
				case len(a.hosts) == 0:
					muted(c, "hosts.json 里没有主机")
				case len(visible) == 0:
					muted(c, "没有匹配的主机")
				default:
					for _, h := range visible {
						a.hostRow(c, h)
					}
				}
				hidden := a.hiddenDeletedCount()
				if hidden > 0 && !a.showDeleted() {
					muted(c, fmt.Sprintf("已隐藏 %d 条已删除(回收站)的连接，勾选上方“显示已删除(回收站)”可查看。", hidden))
				}
			})
		})

		// 分隔线
		ui.Box(c).FillWidth().Height(1).Background(c.Theme().Border)

		// 详情
		a.detail(c)

		// 状态栏
		if a.status != "" {
			s := ui.Text(c, a.status).FontSize(12)
			if a.statusE {
				s.TextColor(c.Theme().Danger)
			} else {
				s.TextColor(c.Theme().Accent)
			}
		}
	})
}

// statsText 是列表上方的统计摘要。
func (a *app) statsText() string {
	total, stored, orphan := 0, 0, 0
	for _, h := range a.hosts {
		total++
		switch {
		case a.stored[loginKey(h)]:
			stored++
		case a.hasAnySecret(h):
			orphan++
		}
	}
	return fmt.Sprintf("共 %d 台 · 已存密码 %d · 残留凭据 %d", total, stored, orphan)
}

func (a *app) hostRow(c *ui.Context, h host) {
	sel := h.ID == a.selID
	row := ui.Row(c.Key("row-"+h.ID)).Gap(10).AlignItems(ui.Center).
		Padding(6, 10).Radius(6).FillWidth().
		OnClick(func() { a.selectHost(h.ID) })
	if sel {
		row.Background(c.Theme().Accent.Alpha(0.15))
	}
	row.Children(func() {
		ui.Text(c, h.Name).Bold().Shrink(0)
		ui.Text(c, fmt.Sprintf("%s@%s:%d", h.User, h.Addr, h.Port)).
			TextColor(c.Theme().TextMuted).Shrink(0)
		if fp := a.folderText(h); fp != "" {
			ui.Text(c, fp).FontSize(12).TextColor(c.Theme().TextMuted).Shrink(0)
		}
		if h.Deleted() {
			ui.Text(c, "已删除(回收站)").TextColor(c.Theme().Danger).FontSize(12).Shrink(0)
		}
		ui.Box(c).Grow(1)
		auth := h.Auth
		switch {
		case h.Auth == "agent":
			muted(c, "agent 认证，无密码")
		case a.stored[loginKey(h)]:
			ui.Text(c, "已存密码 ✓").TextColor(c.Theme().Accent).FontSize(12).Shrink(0)
		case a.hasAnySecret(h):
			ui.Text(c, "残留凭据 ⚠").TextColor(c.Theme().Danger).FontSize(12).Shrink(0)
		default:
			muted(c, "未存（"+auth+"）")
		}
	})
}

func (a *app) detail(c *ui.Context) {
	h, ok := a.selected()
	if !ok {
		muted(c, "点击上方主机查看 / 修改它的密码")
		return
	}
	key := loginKey(h)

	// 详情卡片：圆角浅底，与列表区分
	ui.Column(c.Key("detail")).Gap(8).FillWidth().Padding(12).Radius(8).
		Background(c.Theme().Border.Alpha(0.15)).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, h.Name).Bold()
			sum := fmt.Sprintf("%s@%s:%d · 认证: %s · 键: %s", h.User, h.Addr, h.Port, h.Auth, key)
			if fp := a.folderText(h); fp != "" {
				sum += " · " + fp
			}
			muted(c, sum)
			ui.Box(c).Grow(1)
			if ui.Button(c, "关闭").Clicked() {
				a.selectHost("") // 清空选中即关闭详情，回到纯列表
			}
		})

		if h.Deleted() {
			ui.Text(c, "此连接在 Finallyshell 里已被删除（在它的回收站里）。它的凭据往往还留在系统凭据管理器中，可用下面的“删除全部凭据”清理。").
				TextColor(c.Theme().Danger).FontSize(12)
		}

		// 各凭据记录的存在情况
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			muted(c, "系统中的记录:")
			for _, name := range secretNames {
				k := "host/" + h.ID + "/" + name
				if a.stored[k] {
					ui.Text(c, name+" ✓").TextColor(c.Theme().Accent).FontSize(12).Shrink(0)
				} else {
					ui.Text(c, name+" —").TextColor(c.Theme().TextMuted).FontSize(12).Shrink(0)
				}
			}
		})

		if key == "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				muted(c, "这台主机用 SSH agent 认证，Finallyshell 不为它存登录密码。")
				ui.Box(c).Grow(1)
				if a.hasAnySecret(h) {
					if ui.Button(c, "删除全部凭据").Clicked() {
						a.confirmDelAll(h)
					}
				}
			})
			return
		}

		// 当前密码
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, "当前密码:").Shrink(0)
			if a.reveal {
				ui.Text(c, a.secret).Font("monospace").SingleLine()
			} else {
				ui.Text(c, "••••••••").Font("monospace").TextColor(c.Theme().TextMuted)
			}
			if !a.reveal {
				if ui.Button(c, "查看").Clicked() {
					a.revealSecret(h, key)
				}
			} else {
				if ui.Button(c, "隐藏").Clicked() {
					a.hideSecret()
				}
			}
		})

		// 修改密码
		ui.Field(c, "新密码（保存后下次连接生效）", func() {
			ui.TextInput(c, &a.newVal).Password()
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "保存新密码").Clicked() {
				a.saveNew(h, key)
			}
			if a.stored[key] {
				if ui.Button(c, "删除登录密码").Clicked() {
					a.delStored(h, key)
				}
			}
			ui.Box(c).Grow(1) // 危险操作与常规操作分开，靠右
			if ui.Button(c, "删除全部凭据").Clicked() {
				a.confirmDelAll(h)
			}
		})
		muted(c, "注意：这里改的只是本机存的密码；服务器上的真实密码要在服务器上改（passwd）。")
	})
}

// ---- 入口 ----

func runGUI() {
	mygo.App.WhenReady(func() {
		cfg := loadConfig()
		a := &app{stored: map[string]bool{}, cfg: cfg, showDeletedView: !cfg.HideDeleted}
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:  "Finallyshell 凭据迁移工具 (FinallyMigrate)",
			Width:  860,
			Height: 560,
			Content: ui.View(a.view),
		})
		a.win = win
		setIcon(win)
		a.reload()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
