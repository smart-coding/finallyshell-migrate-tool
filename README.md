# FinallyMigrate

**Finallyshell 凭据迁移工具** —— 查看 / 管理 [Finallyshell](https://github.com/TooonyChen/finallyshell) 存在 Windows 凭据管理器里的密码，并把 [FinalShell](https://www.hostbuf.com/) 里保存的密码迁移（补写）过来，迁移完成后连接不再需要手动输入密码。

![icon](assets/icon.png)

## 背景

[Finallyshell](https://github.com/TooonyChen/finallyshell) 是 FinalShell 的开源复刻，界面布局一致。它自带的导入功能可以把 FinalShell 的连接信息搬过来，但**不迁移密码**——连接时会逐台重新询问。

FinallyMigrate 补上这一环：

1. 在 Finallyshell 的连接管理器里，用它自带的导入功能导入 FinalShell 连接（此时缺密码）
2. 用本工具选择 FinalShell 数据目录，自动解密并逐条比对，把缺失的密码写进系统凭据管理器
3. 完成——Finallyshell 里直接连接，无需再输密码

## 功能

- **凭据管理**：列出 Finallyshell 的所有连接及各自凭据的存放状态；查看、修改、删除密码（含回收站主机残留凭据的清理）
- **密码补写**：递归读取 FinalShell 的 `conn` 目录（含分组子目录，解析 `folder.json` 真实分组名），解密连接里保存的密码，按 `地址 + 端口 + 用户` 与 Finallyshell 现有主机逐条比对后补写
- **勾选导入**：扫描清单逐条标注判定结果（可补写 / 已存密码不覆盖 / 未找到主机 / 跳过原因），支持全选、逐条勾选后再确认执行
- **安全约束**：已存密码绝不覆盖；密码解密只驻内存，不打印、不写日志、不落盘

## 环境说明

> **目前仅在 Windows 11 下测试通过。其他 Windows 版本及其他操作系统均未验证**（代码本身无系统特定依赖，理论可跨平台编译），欢迎反馈。

使用前提：Windows 凭据管理器（Finallyshell 的密码存储位置）、已安装 [Go](https://go.dev/dl/)（仅构建时需要）。

## 构建

```bash
go build -o finallymigrate.exe .
```

仓库内的 `rsrc_windows_amd64.syso` 已包含应用图标资源，构建产物自动带图标；修改 `assets/` 图标后可用 `go run ./tools/genicon` 重新生成，再用 [rsrc](https://github.com/akavel/rsrc) 重做 `.syso`。

## 用法

### 图形界面

双击 `finallymigrate.exe`（或运行 `finallymigrate-gui.exe`）：

- 主窗口：凭据列表（支持搜索过滤、隐藏已删除连接）、查看 / 修改 / 删除凭据、点击「从 FinalShell 补密码」进入迁移窗口
- 迁移窗口：点「选择目录…」选定 FinalShell 数据目录（含 `conn` 子目录）→ 自动扫描比对 → 勾选要补写的条目 → 执行

### 命令行

```bash
FinallyMigrate                                  # 打开图形界面
FinallyMigrate list                             # 列出主机与凭据存放状态
FinallyMigrate read <key|关键字>                 # 读取凭据原文
FinallyMigrate migrate-finalshell [目录] [-y]    # 从 FinalShell 补写密码（先出比对计划）
```

`migrate-finalshell` 的目录不传时自动探测常见安装位置。

## 数据存储说明

| 数据 | 位置 |
|---|---|
| 连接信息 | `%APPDATA%\finallyshell\hosts.json`（Finallyshell 管理） |
| 密码 | Windows 凭据管理器（service `finallyshell`，键 `host/<id>/password` 等） |
| 本工具偏好 | `%APPDATA%\FinallyMigrate\config.json` |

本工具与 Finallyshell 共享同一凭据存储路径，读写方式与其源码 `keychain.go` 一致。

## 许可

[PolyForm Noncommercial License 1.0.0](LICENSE) —— 个人使用、公司内部使用免费；商业分发需另行授权。
