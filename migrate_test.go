package main

// planAgainst 的扫描比对测试：连接判定、目录递归、主机匹配与动作。
// 用合成的主机列表，不依赖真实 hosts.json 和凭据管理器（只读不写）。

import (
	"crypto/des"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// deletedTestTime 是一个非零时间，表示主机在回收站里。
func deletedTestTime() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// fsEncodeTestPass 构造一个 FinalShell 格式的合法密文（fsDecodePass 的
// 逆过程），供测试夹具使用：head 8 字节自洽派生密钥 + DES-ECB + PKCS5。
func fsEncodeTestPass(plain string) string {
	pad := des.BlockSize - len(plain)%des.BlockSize
	data := make([]byte, len(plain)+pad)
	copy(data, plain)
	for i := len(plain); i < len(data); i++ {
		data[i] = byte(pad)
	}
	head := []byte{3, 1, 4, 1, 5, 9, 2, 6} // head[5]=9，派生路径确定
	key := fsRanDomKey(head)
	// 与 fsDESDecode 一致：派生出的 16 字节 MD5 只取前 8 字节当 DES 密钥
	if len(key) > 8 {
		key = key[:8]
	}
	block, err := des.NewCipher(key)
	if err != nil {
		panic(err)
	}
	enc := make([]byte, len(data))
	for i := 0; i < len(data); i += des.BlockSize {
		block.Encrypt(enc[i:], data[i:i+des.BlockSize])
	}
	return base64.StdEncoding.EncodeToString(append(head, enc...))
}

func TestFsEncodeTestPassRoundTrip(t *testing.T) {
	for _, pw := range []string{"abc", "p@ss w0rd!", "中文密码", "012345678901234567"} {
		got, err := fsDecodePass(fsEncodeTestPass(pw))
		if err != nil || got != pw {
			t.Errorf("round trip %q: got %q err %v", pw, got, err)
		}
	}
}

// writeConn 往临时 FinalShell 目录里放一个连接文件（可带子目录）。
func writeConn(t *testing.T, fsDir, dir, file, body string) {
	t.Helper()
	writeFile(t, filepath.Join(fsDir, "conn"), filepath.Join(dir, file), body)
}

// writeFile 往临时目录里放一个任意文件（可带子目录），folder.json 等用。
func writeFile(t *testing.T, baseDir, rel, body string) {
	t.Helper()
	p := filepath.Join(baseDir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlanAgainst(t *testing.T) {
	fsDir := t.TempDir()
	writeConn(t, fsDir, "", "a_connect_config.json", `{
		"id": "1", "name": "缺密码机", "host": "10.0.0.1", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": "`+fsEncodeTestPass("pw1")+`"
	}`)
	writeConn(t, fsDir, "", "b_connect_config.json", `{
		"id": "2", "name": "已删机", "host": "10.0.0.2", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": "`+fsEncodeTestPass("pw2")+`", "delete_time": 1700000000000
	}`)
	writeConn(t, fsDir, "", "c_connect_config.json", `{
		"id": "3", "name": "端口字符串", "host": "10.0.0.3", "port": "2222",
		"user_name": "ops", "conection_type": 100, "authentication_type": 2,
		"password": "x"
	}`)
	writeConn(t, fsDir, "生产环境", "d_connect_config.json", `{
		"id": "4", "name": "组内缺密码机", "host": "10.0.0.4", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": "`+fsEncodeTestPass("pw4")+`"
	}`)
	writeConn(t, fsDir, filepath.Join("生产环境", "数据库"), "e_connect_config.json", `{
		"id": "5", "name": "嵌套缺密码机", "host": "10.0.0.5", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": "`+fsEncodeTestPass("pw5")+`"
	}`)
	writeConn(t, fsDir, "", "f_connect_config.json", `{
		"id": "6", "name": "空密码机", "host": "10.0.0.6", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": ""
	}`)
	writeConn(t, fsDir, "生产环境", "notes.txt", "hello") // 非连接文件应被忽略

	hosts := []host{
		{ID: "hLive", Name: "Web01", Addr: "10.0.0.1", Port: 22, User: "root", Auth: "password"},
		{ID: "hTrashed", Name: "旧库", Addr: "10.0.0.5", Port: 22, User: "root", Auth: "password",
			DeletedAt: deletedTestTime()},
		{ID: "hNoPw", Name: "待补机", Addr: "10.0.0.6", Port: 22, User: "root", Auth: "password"},
	}

	items, err := planAgainst(hosts, fsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 6 {
		t.Fatalf("期望 6 条，得到 %d 条: %+v", len(items), items)
	}
	byName := map[string]planItem{}
	for _, it := range items {
		byName[it.Name] = it
	}

	// 匹配到未删除主机且可解密 → fill，HostID 指向匹配的主机
	it := byName["缺密码机"]
	if it.Action != actionFill || !it.importable() || it.HostID != "hLive" {
		t.Errorf("缺密码机应为 fill 且匹配 hLive，得到 %s/%s/%s", it.Action, it.Note, it.HostID)
	}
	if it.Folder != "" {
		t.Errorf("顶层机 Folder 应为空，得到 %q", it.Folder)
	}
	if it.Addr != "10.0.0.1:22" {
		t.Errorf("地址解析错: %s", it.Addr)
	}

	// FinalShell 里已删除 → 跳过
	if it := byName["已删机"]; it.Action != actionSkip || it.importable() {
		t.Errorf("已删机应跳过（FinalShell 里已删除），得到 %s/%s", it.Action, it.Note)
	}

	// 非密码认证跳过；端口是字符串形态也要解析出 2222
	if it := byName["端口字符串"]; it.Action != actionSkip {
		t.Errorf("公钥认证主机应跳过，得到 %s/%s", it.Action, it.Note)
	} else if it.Addr != "10.0.0.3:2222" {
		t.Errorf("字符串端口解析错: %s", it.Addr)
	}

	// 未导入到 Finallyshell 的主机 → no-match（即使 FinalShell 有密码）
	if it := byName["组内缺密码机"]; it.Action != actionNoMatch {
		t.Errorf("主机列表里没有 10.0.0.4 时应为 no-match，得到 %s/%s", it.Action, it.Note)
	}

	// 匹配到主机但 FinalShell 里没存密码 → 跳过
	if it := byName["空密码机"]; it.Action != actionSkip || it.importable() {
		t.Errorf("空密码机应跳过（FinalShell 里没存密码），得到 %s/%s", it.Action, it.Note)
	}

	// 只匹配到回收站里的主机 → 跳过
	if it := byName["嵌套缺密码机"]; it.Action != actionSkip {
		t.Errorf("只匹配到回收站主机应跳过，得到 %s/%s", it.Action, it.Note)
	} else if it.Folder != filepath.ToSlash("生产环境/数据库") {
		t.Errorf("嵌套机 Folder 应为 生产环境/数据库，得到 %q", it.Folder)
	}
}

func TestPlanAgainstFolderJSON(t *testing.T) {
	// 真实 FinalShell 数据目录：磁盘目录名是分组的编码 ID，真实名称在
	// folder.json；连接与分组的嵌套靠 parent_id。
	fsDir := t.TempDir()
	writeConn(t, fsDir, "1699000000001", "a_connect_config.json", `{
		"id": "1", "name": "编组顶层机", "host": "10.1.0.1", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": "`+fsEncodeTestPass("pw")+`", "parent_id": "foldA"
	}`)
	writeFile(t, filepath.Join(fsDir, "conn"), filepath.Join("1699000000001", "folder.json"),
		`{"id": "foldA", "name": "生产环境", "parent_id": "root"}`)
	writeConn(t, fsDir, filepath.Join("1699000000001", "1699000000002"), "b_connect_config.json", `{
		"id": "2", "name": "编组嵌套机", "host": "10.1.0.2", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": "`+fsEncodeTestPass("pw")+`", "parent_id": "foldB"
	}`)
	writeFile(t, filepath.Join(fsDir, "conn"), filepath.Join("1699000000001", "1699000000002", "folder.json"),
		`{"id": "foldB", "name": "数据库", "parent_id": "foldA"}`)
	// parent_id 为空：按所在目录的分组回退
	writeConn(t, fsDir, "1699000000001", "c_connect_config.json", `{
		"id": "3", "name": "无父机", "host": "10.1.0.3", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": "`+fsEncodeTestPass("pw")+`"
	}`)
	// 已删除的分组：名字不进路径
	writeConn(t, fsDir, "1699000000003", "d_connect_config.json", `{
		"id": "4", "name": "废组机", "host": "10.1.0.4", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": "`+fsEncodeTestPass("pw")+`"
	}`)
	writeFile(t, filepath.Join(fsDir, "conn"), filepath.Join("1699000000003", "folder.json"),
		`{"id": "foldDead", "name": "废弃分组", "parent_id": "root", "delete_time": 1700000000000}`)

	items, err := planAgainst(nil, fsDir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]planItem{}
	for _, it := range items {
		byName[it.Name] = it
	}
	if it := byName["编组顶层机"]; it.Folder != "生产环境" {
		t.Errorf("folder.json 名称应替换编码目录名，得到 %q", it.Folder)
	}
	if it := byName["编组嵌套机"]; it.Folder != "生产环境/数据库" {
		t.Errorf("parent_id 链应解出 生产环境/数据库，得到 %q", it.Folder)
	}
	if it := byName["无父机"]; it.Folder != "生产环境" {
		t.Errorf("parent_id 为空应回退所在目录分组，得到 %q", it.Folder)
	}
	if it := byName["废组机"]; it.Folder != "" {
		t.Errorf("已删除分组的名字不应出现在路径里，得到 %q", it.Folder)
	}
}

func TestPlanAgainstPort22Match(t *testing.T) {
	// Finallyshell 存主机时对 22 端口省略 port 字段（读回来是 0），
	// 与 FinalShell 的 22 应视为相同端口。
	fsDir := t.TempDir()
	writeConn(t, fsDir, "", "a_connect_config.json", `{
		"id": "1", "name": "默认端口机", "host": "10.2.0.1", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": "`+fsEncodeTestPass("pw")+`"
	}`)
	hosts := []host{
		{ID: "h22", Name: "省略端口的机", Addr: "10.2.0.1", Port: 0, User: "root", Auth: "password"},
	}
	items, err := planAgainst(hosts, fsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != actionFill || items[0].HostID != "h22" {
		t.Errorf("端口 0/22 应匹配为 fill，得到 %+v", items)
	}
}

func TestPlanAgainstNoMatchWhenEmpty(t *testing.T) {
	fsDir := t.TempDir()
	writeConn(t, fsDir, "", "a_connect_config.json", `{
		"id": "1", "name": "任意机", "host": "10.9.9.9", "port": 22,
		"user_name": "root", "conection_type": 100, "authentication_type": 1,
		"password": ""
	}`)
	items, err := planAgainst(nil, fsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != actionNoMatch {
		t.Fatalf("空主机列表应全部 no-match，得到 %+v", items)
	}
}

func TestFindDupPrefersLive(t *testing.T) {
	hosts := []host{
		{ID: "hTrash", Addr: "10.0.0.1", Port: 22, User: "root", DeletedAt: deletedTestTime()},
		{ID: "hLive", Addr: "10.0.0.1", Port: 22, User: "root"},
	}
	if h := findDup(hosts, "10.0.0.1", 22, "root"); h == nil || h.ID != "hLive" {
		t.Errorf("应优先匹配未删除主机，得到 %+v", h)
	}
	// 大小写不敏感
	if h := findDup(hosts, "10.0.0.1", 22, "ROOT"); h == nil || h.ID != "hLive" {
		t.Errorf("用户名匹配应大小写不敏感，得到 %+v", h)
	}
	// 只有回收站主机时返回它
	only := hosts[:1]
	if h := findDup(only, "10.0.0.1", 22, "root"); h == nil || h.ID != "hTrash" {
		t.Errorf("只有回收站主机时应返回它，得到 %+v", h)
	}
}

func TestPlanAgainstMissingConn(t *testing.T) {
	if _, err := planAgainst(nil, t.TempDir()); err == nil {
		t.Fatal("没有 conn 子目录时应报错")
	}
}

func TestFolderPath(t *testing.T) {
	folders := []folder{
		{ID: "fA", Name: "组A"},
		{ID: "fB", Name: "组B", Parent: "fA"},
		{ID: "fC", Name: "组C", Parent: "fB"},
		{ID: "fX", Name: "坏链", Parent: "fGhost"}, // 父 ID 不存在
		{ID: "fY", Name: "环1", Parent: "fZ"},
		{ID: "fZ", Name: "环2", Parent: "fY"}, // 链上成环
	}
	cases := []struct {
		id   string
		want string
	}{
		{"", ""},
		{"fNone", ""},       // 未知 ID
		{"fA", "组A"},        // 顶层
		{"fB", "组A/组B"},     // 两级
		{"fC", "组A/组B/组C"}, // 三级
		{"fX", "坏链"},       // 父链断了只显示自己
		{"fY", "环2/环1"},    // 成环时截断不挂死
	}
	for _, c := range cases {
		if got := folderPath(folders, c.id); got != c.want {
			t.Errorf("folderPath(%q) = %q，期望 %q", c.id, got, c.want)
		}
	}
}
