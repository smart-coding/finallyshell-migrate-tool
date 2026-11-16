package main

// “从 FinalShell 补写密码”窗口：点按钮选目录（类似文件上传）→ 选定后自动
// 扫描比对（逐条标注结果，不显示密码明文）→ 勾选要补写的条目 → 执行补写
// → 回主窗口刷新。连接信息本身由 Finallyshell 自带的导入功能搬入。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type importState struct {
	a   *app // 主窗口
	win *mygo.Window

	path   string
	scans  string // "", "running", "done", "error"
	scanEr string
	items  []planItem
	adding int  // 可补写条数（fill）
	pickEr string // 目录选择/校验的错误提示

	importing   bool
	result      *migrateResult
	resultEr    string
	notImported int // 可补写但因未勾选而没补的条数（用于结果提示）
}

func (a *app) openImport() {
	if a.imp != nil && a.impWin != nil {
		a.impWin.Focus()
		return
	}
	is := &importState{a: a, path: findFinalShellDir()}
	a.imp = is
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     "从 FinalShell 补写密码到 Finallyshell",
		Width:     780,
		Height:    540,
		MinWidth:  620,
		MinHeight: 420,
		Content:   ui.View(is.view),
	})
	is.win = win
	setIcon(win)
	a.impWin = win
	win.OnClosed(func() {
		a.impWin = nil
		a.imp = nil
	})
}

func (is *importState) post(fn func()) {
	if is.win != nil {
		is.win.Update(fn)
	}
}

// selectedCount 是当前勾选的可补写条数。
func (is *importState) selectedCount() int {
	n := 0
	for i := range is.items {
		if is.items[i].importable() && is.items[i].Selected {
			n++
		}
	}
	return n
}

// selectAll 只作用于可补写条目（其余条目没有勾选框）。
func (is *importState) selectAll(v bool) {
	is.post(func() {
		for i := range is.items {
			if is.items[i].importable() {
				is.items[i].Selected = v
			}
		}
	})
}

// pickDir 弹原生目录选择对话框；选定并校验通过后自动扫描。
func (is *importState) pickDir() {
	go func() {
		res, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      is.win,
			Title:       "选择 FinalShell 数据目录（含 conn 子目录）",
			DefaultPath: is.path,
			Directory:   true,
		})
		if err != nil {
			is.post(func() { is.pickEr = "打开目录选择失败: " + err.Error() })
			return
		}
		if len(res) == 0 {
			return // 用户取消
		}
		dir := res[0]
		if st, serr := os.Stat(filepath.Join(dir, "conn")); serr != nil || !st.IsDir() {
			is.post(func() { is.pickEr = "所选目录没有 conn 子目录，不是 FinalShell 数据目录：" + dir })
			return
		}
		is.post(func() {
			is.path, is.pickEr = dir, ""
			is.scan() // 选定即扫描
		})
	}()
}

func actionLabel(it planItem) string {
	switch it.Action {
	case actionFill:
		if it.Note != "" {
			return "补写密码（" + it.Note + "）"
		}
		return "补写密码"
	case actionHasPw:
		return "已存密码，跳过（不覆盖）"
	case actionNoMatch:
		return "未找到主机：请先在 Finallyshell 里导入这条连接"
	default:
		if it.Note != "" {
			return "跳过：" + it.Note
		}
		return "跳过"
	}
}

func actionColor(c *ui.Context, action string) ui.Color {
	switch action {
	case actionFill:
		return c.Theme().Accent
	case actionHasPw, actionNoMatch:
		return c.Theme().TextMuted
	default:
		return c.Theme().Danger
	}
}

func (is *importState) view(c *ui.Context) {
	ui.Column(c).Gap(10).Padding(14).Fill().Children(func() {
		ui.Text(c, "从 FinalShell 补写密码").Bold().FontSize(15)
		muted(c, "第 1 步：在 Finallyshell 的连接管理器里用它自带的导入功能导入 FinalShell 连接（不含密码）。第 2 步（本工具）：选 FinalShell 数据目录，逐条比对，把缺失的密码写进系统凭据管理器。只写凭据、不改 hosts.json，Finallyshell 运行中也可以执行。")

		// 第一步：点按钮选目录
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "选择目录…").Clicked() {
				is.pickDir()
			}
			muted(c, "选择 FinalShell 数据目录（含 conn 子目录），选定后自动扫描比对")
		})
		if is.pickEr != "" {
			ui.Text(c, is.pickEr).TextColor(c.Theme().Danger).FontSize(12)
		}

		// 兜底：手动输入/修改路径后点扫描
		ui.Field(c, "当前路径（也可手动修改后点扫描）", func() {
			ui.TextInput(c, &is.path)
		})

		// 扫描行
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			label := "扫描"
			if is.scans == "done" || is.scans == "error" {
				label = "重新扫描"
			}
			if ui.Button(c, label).Clicked() {
				is.scan()
			}
			switch is.scans {
			case "running":
				muted(c, "扫描中…")
			case "done":
				cnt := actionCounts(is.items)
				muted(c, fmt.Sprintf("共 %d 条：可补写 %d · 已存密码 %d · 未找到主机 %d · 跳过 %d",
					len(is.items), cnt[actionFill], cnt[actionHasPw], cnt[actionNoMatch], cnt[actionSkip]))
			case "error":
				ui.Text(c, "扫描失败: "+is.scanEr).TextColor(c.Theme().Danger).FontSize(12)
			}
		})

		// 勾选 + 执行行
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if is.scans == "done" {
				if ui.Button(c, "全选").Clicked() {
					is.selectAll(true)
				}
				if ui.Button(c, "全不选").Clicked() {
					is.selectAll(false)
				}
				muted(c, fmt.Sprintf("已勾选 %d / %d 条", is.selectedCount(), is.adding))
			}
			ui.Box(c).Grow(1)
			importing := is.importing
			label := fmt.Sprintf("执行补写(%d条)", is.selectedCount())
			if importing {
				label = "补写中…"
			}
			can := !importing && is.scans == "done" && is.selectedCount() > 0
			if ui.Button(c, label).Disabled(!can).Clicked() && can {
				is.run()
			}
		})

		// 补写结果摘要
		if is.resultEr != "" {
			ui.Text(c, "补写失败: "+is.resultEr).TextColor(c.Theme().Danger).FontSize(12)
		}
		if r := is.result; r != nil {
			ui.Column(c).Gap(4).FillWidth().Children(func() {
				ui.Text(c, fmt.Sprintf("补写完成：写入密码 %d 条。现在可以在 Finallyshell 里直接连接这些主机。", r.SecretsOK)).
					TextColor(c.Theme().Accent)
				if is.notImported > 0 {
					muted(c, fmt.Sprintf("另有 %d 条可补写条目未勾选，没有补写。", is.notImported))
				}
				for _, s := range r.SecretErr {
					ui.Text(c, "密码写入失败: "+s).TextColor(c.Theme().Danger).FontSize(12)
				}
				muted(c, "若个别主机连接时仍询问密码，重启 Finallyshell 后再试。")
			})
		}

		// 清单
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).Gap(2).FillWidth().Children(func() {
				for i := range is.items {
					is.itemRow(c, &is.items[i])
				}
			})
		})
	})
}

func (is *importState) itemRow(c *ui.Context, it *planItem) {
	// 行 key 带上目录：不同分组下可能有同名同址的连接
	ui.Row(c.Key("imp-"+it.Folder+"/"+it.Name+it.Addr)).Gap(10).AlignItems(ui.Center).
		Padding(5, 10).Radius(5).FillWidth().Children(func() {
		// 可补写条目用勾选框；其余用彩色圆点标注结果
		if it.importable() {
			ui.Checkbox(c, &it.Selected, "")
		} else {
			ui.Text(c, "●").Shrink(0).TextColor(actionColor(c, it.Action))
		}
		ui.Text(c, it.Name).Bold().Shrink(0)
		if it.Folder != "" {
			ui.Text(c, it.Folder).FontSize(12).TextColor(c.Theme().TextMuted).Shrink(0)
		}
		ui.Text(c, it.User+"@"+it.Addr).TextColor(c.Theme().TextMuted).Shrink(0)
		ui.Box(c).Grow(1)
		ui.Text(c, actionLabel(*it)).FontSize(12).Shrink(0).
			TextColor(actionColor(c, it.Action))
	})
}

// scan 后台扫描 + 解密 + 比对（不写任何东西），回 UI 渲染清单；可补写条目默认全选。
func (is *importState) scan() {
	dir := strings.TrimSpace(is.path)
	is.post(func() {
		is.scans, is.items, is.adding, is.scanEr, is.result, is.resultEr = "running", nil, 0, "", nil, ""
	})
	go func() {
		items, err := planMigration(dir)
		is.post(func() {
			if err != nil {
				is.scans, is.scanEr = "error", err.Error()
				return
			}
			is.items = items
			for i := range items {
				if items[i].importable() {
					items[i].Selected = true
					is.adding++
				}
			}
			is.scans = "done"
		})
	}()
}

// run 补写勾选的条目，回 UI 显示结果并刷新主窗口列表。
func (is *importState) run() {
	var sel []planItem
	notImported := 0
	for i := range is.items {
		it := &is.items[i]
		if !it.importable() {
			continue
		}
		if it.Selected {
			sel = append(sel, *it)
		} else {
			notImported++
		}
	}
	is.post(func() {
		is.importing, is.notImported, is.result, is.resultEr = true, notImported, nil, ""
	})
	go func() {
		res, err := fillPasswords(sel)
		is.post(func() {
			is.importing = false
			if err != nil {
				is.resultEr = err.Error()
				return
			}
			is.result = res
			is.a.reload() // 主窗口列表刷新
		})
	}()
}
