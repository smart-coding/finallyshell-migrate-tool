// genicon 程序化绘制 FinallyMigrate 应用图标（纯 Go，无第三方依赖）。
//
// 设计（与 finallyshell 官方图标同族但可区分）：
//   - 同：深色圆角方块底、左侧栏带连接卡片、右侧主体元素、光标胶囊
//   - 异：侧栏青绿渐变（官方为蓝）；主体是白色钥匙（凭据）；胶囊改成
//     青绿色右向迁移箭头
//
// 运行（在模块根目录）：
//
//	go run ./tools/genicon
//
// 输出到 assets/：icon.ico（16~256 多尺寸）、icon.png（256，运行时
// 窗口图标用）、icon.svg（设计源文件）。生成物直接进仓库，构建不依赖
// 本工具。
//
// 绘制方式：每个目标尺寸按 4x 超采样渲染再盒式降采样抗锯齿；圆角矩形
// 用有符号距离场判定；渐变逐像素竖向插值。
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
)

// ---- 设计常量（1024 画布，与 finallyshell 官方画布一致）----

const (
	canvas = 1024.0
	ss     = 4 // 超采样倍数
)

var (
	// 颜色（官方同款底色 + 青绿区分色）
	bgTop     = hexRGB("#2d3035")
	bgBottom  = hexRGB("#1a1c1f")
	sidebarTop    = hexRGB("#14b8a6")
	sidebarBottom = hexRGB("#0f766e")
	white         = hexRGB("#ffffff")
	pillTeal      = hexRGB("#5eead4")
	greenDot      = hexRGB("#22c55e")
	glyph         = hexRGB("#e5e7eb")
	tealArrow     = hexRGB("#0d9488")
)

// hexRGB 解析 "#rrggbb"。
func hexRGB(s string) [3]float64 {
	var r, g, b int
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return [3]float64{float64(r), float64(g), float64(b)}
}

// lerpRGB 竖向插值：y0..y1 之间从 a 到 b。
func lerpRGB(a, b [3]float64, y, y0, y1 float64) [3]float64 {
	t := (y - y0) / (y1 - y0)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return [3]float64{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}
}

// ---- 图层：覆盖判定 + 颜色（可随 y 渐变）+ 不透明度 ----

type layer struct {
	cover func(x, y float64) bool
	col   func(y float64) [3]float64
	op    float64 // 0..1
}

func solid(c [3]float64) func(float64) [3]float64 {
	return func(float64) [3]float64 { return c }
}

// inSquare 是 1024 画布中央的圆角方块（官方同款几何）。
func inSquare(x, y float64) bool {
	return inRoundedRect(x, y, 100, 100, 824, 824, 185)
}

// layers 是图标的全部图层，按从底到顶的顺序。
func layers() []layer {
	return []layer{
		// 深色背景（官方同款渐变）
		{cover: inSquare, col: func(y float64) [3]float64 { return lerpRGB(bgTop, bgBottom, y, 100, 924) }, op: 1},
		// 左侧栏：青绿渐变（与官方蓝色的区分点），右缘为直线（被方块裁剪）
		{cover: func(x, y float64) bool { return inSquare(x, y) && x <= 350 },
			col: func(y float64) [3]float64 { return lerpRGB(sidebarTop, sidebarBottom, y, 100, 924) }, op: 1},
		// 侧栏卡片：首张白色实心 + 绿色状态点 + 浅青胶囊
		{cover: func(x, y float64) bool { return inRoundedRect(x, y, 150, 300, 150, 78, 22) }, col: solid(white), op: 1},
		{cover: func(x, y float64) bool { return inCircle(x, y, 186, 339, 14) }, col: solid(greenDot), op: 1},
		{cover: func(x, y float64) bool { return inRoundedRect(x, y, 212, 330, 66, 18, 9) }, col: solid(pillTeal), op: 1},
		// 其余两张半透明卡片（层级感）
		{cover: func(x, y float64) bool { return inRoundedRect(x, y, 150, 420, 150, 78, 22) }, col: solid(white), op: 0.18},
		{cover: func(x, y float64) bool { return inRoundedRect(x, y, 150, 540, 150, 78, 22) }, col: solid(white), op: 0.18},
		// 主体：白色钥匙（环 + 柄 + 两齿，圆头粗线条）
		{cover: func(x, y float64) bool { return inRing(x, y, 475, 450, 52, 108) }, col: solid(glyph), op: 1},
		{cover: func(x, y float64) bool { return inSegment(x, y, 555, 450, 795, 450, 28) }, col: solid(glyph), op: 1},
		{cover: func(x, y float64) bool { return inSegment(x, y, 690, 450, 690, 530, 28) }, col: solid(glyph), op: 1},
		{cover: func(x, y float64) bool { return inSegment(x, y, 765, 450, 765, 530, 28) }, col: solid(glyph), op: 1},
		// 迁移箭头：青绿胶囊 + 三角箭尖（替代官方灰色光标胶囊）
		{cover: func(x, y float64) bool { return inRoundedRect(x, y, 610, 574, 180, 56, 28) }, col: solid(tealArrow), op: 1},
		{cover: func(x, y float64) bool { return inTriangle(x, y, 775, 556, 775, 648, 835, 602) }, col: solid(tealArrow), op: 1},
	}
}

// ---- 几何判定 ----

// inRoundedRect 圆角矩形（有符号距离场思路：角落用圆判定）。
func inRoundedRect(x, y, x0, y0, w, h, r float64) bool {
	if x < x0 || x > x0+w || y < y0 || y > y0+h {
		return false
	}
	dx := max(x0+r-x, x-(x0+w-r), 0)
	dy := max(y0+r-y, y-(y0+h-r), 0)
	return dx*dx+dy*dy <= r*r
}

func max(a, b, c float64) float64 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

// inCircle 实心圆。
func inCircle(x, y, cx, cy, r float64) bool {
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

// inRing 圆环（内外半径之间）。
func inRing(x, y, cx, cy, rIn, rOut float64) bool {
	dx, dy := x-cx, y-cy
	d2 := dx*dx + dy*dy
	return d2 >= rIn*rIn && d2 <= rOut*rOut
}

// inSegment 圆头粗线段：点到线段距离 ≤ 半宽。
func inSegment(x, y, x1, y1, x2, y2, halfW float64) bool {
	dx, dy := x2-x1, y2-y1
	l2 := dx*dx + dy*dy
	var t float64
	if l2 == 0 {
		t = 0
	} else {
		t = ((x-x1)*dx + (y-y1)*dy) / l2
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
	}
	px, py := x1+t*dx, y1+t*dy
	ex, ey := x-px, y-py
	return ex*ex+ey*ey <= halfW*halfW
}

// inTriangle 三角形（重心符号法，顶点按顺时针或逆时针均可）。
func inTriangle(x, y, x1, y1, x2, y2, x3, y3 float64) bool {
	d1 := sign(x, y, x1, y1, x2, y2)
	d2 := sign(x, y, x2, y2, x3, y3)
	d3 := sign(x, y, x3, y3, x1, y1)
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

func sign(px, py, ax, ay, bx, by float64) float64 {
	return (px-bx)*(ay-by) - (ax-bx)*(py-by)
}

// ---- 渲染 ----

// render 画一个目标尺寸的图标：4x 超采样后盒式降采样。
func render(size int) *image.RGBA {
	s4 := size * ss
	scale := canvas / float64(s4)
	layers := layers()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for j := 0; j < size; j++ {
		for i := 0; i < size; i++ {
			var r, g, b, a float64
			for sj := 0; sj < ss; sj++ {
				for si := 0; si < ss; si++ {
					x := (float64(i*ss+si) + 0.5) * scale
					y := (float64(j*ss+sj) + 0.5) * scale
					// 从透明开始按图层顺序 src-over 合成
					var dr, dg, db, da float64
					for _, l := range layers {
						if !l.cover(x, y) {
							continue
						}
						c := l.col(y)
						sa := l.op
						dr = c[0]*sa + dr*(1-sa)
						dg = c[1]*sa + dg*(1-sa)
						db = c[2]*sa + db*(1-sa)
						da = sa + da*(1-sa)
					}
					r, g, b, a = r+dr, g+dg, b+db, a+da
				}
			}
			n := float64(ss * ss)
			off := img.PixOffset(i, j)
			img.Pix[off+0] = clamp8(r / n)
			img.Pix[off+1] = clamp8(g / n)
			img.Pix[off+2] = clamp8(b / n)
			// alpha 按 0..1 累加，输出换算到 0..255
			img.Pix[off+3] = clamp8(a / n * 255)
		}
	}
	return img
}

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5)
}

// ---- ICO 容器 ----

// bmpEntry 把图像转成 ICO 的 BMP（DIB）条目：32 位 BGRA 自下而上 + AND 掩码。
func bmpEntry(img *image.RGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	maskRow := ((w + 31) / 32) * 4
	xorLen := w * h * 4
	buf := make([]byte, 40+xorLen+h*maskRow)
	binary.LittleEndian.PutUint32(buf[0:], 40)               // biSize
	binary.LittleEndian.PutUint32(buf[4:], uint32(w))        // biWidth
	binary.LittleEndian.PutUint32(buf[8:], uint32(h*2))      // biHeight（XOR+AND）
	binary.LittleEndian.PutUint16(buf[12:], 1)               // biPlanes
	binary.LittleEndian.PutUint16(buf[14:], 32)              // biBitCount
	// BI_RGB(0)、biSizeImage 等留 0 即可
	// 像素：BGRA，自下而上
	for j := 0; j < h; j++ {
		src := img.Pix[img.PixOffset(0, h-1-j):][:w*4]
		dst := buf[40+j*w*4:]
		for i := 0; i < w; i++ {
			dst[i*4+0] = src[i*4+2]
			dst[i*4+1] = src[i*4+1]
			dst[i*4+2] = src[i*4+0]
			dst[i*4+3] = src[i*4+3]
		}
	}
	// AND 掩码全 0（有 alpha 通道，掩码不参与）——buf 已初始化为零
	return buf
}

// pngEntry 图像转 PNG 字节。
func pngEntry(img *image.RGBA) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// writeICO 按尺寸写多尺寸 .ico：≤64 用 BMP 条目（兼容性最好），
// 128/256 用 PNG 条目（体积小，Vista+ 支持）。
func writeICO(path string, images map[int]*image.RGBA, pngSizes []int) error {
	var order []int
	var payloads [][]byte
	add := func(size int) {
		var data []byte
		if contains(pngSizes, size) {
			data = pngEntry(images[size])
		} else {
			data = bmpEntry(images[size])
		}
		order = append(order, size)
		payloads = append(payloads, data)
	}
	for _, s := range []int{16, 24, 32, 48, 64, 128, 256} {
		if _, ok := images[s]; ok {
			add(s)
		}
	}

	var out bytes.Buffer
	hdr := make([]byte, 6)
	binary.LittleEndian.PutUint16(hdr[0:], 0) // reserved
	binary.LittleEndian.PutUint16(hdr[2:], 1) // type: icon
	binary.LittleEndian.PutUint16(hdr[4:], uint16(len(payloads)))
	out.Write(hdr)

	// 目录表
	offset := uint32(6 + 16*len(payloads))
	for i, size := range order {
		e := make([]byte, 16)
		wb := byte(size)
		if size >= 256 {
			wb = 0
		}
		e[0], e[1] = wb, wb // width, height
		e[3] = 0            // 颜色数
		binary.LittleEndian.PutUint16(e[4:], 1)
		binary.LittleEndian.PutUint16(e[6:], 32)
		binary.LittleEndian.PutUint32(e[8:], uint32(len(payloads[i])))
		binary.LittleEndian.PutUint32(e[12:], offset)
		offset += uint32(len(payloads[i]))
		out.Write(e)
	}
	for _, p := range payloads {
		out.Write(p)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func main() {
	out := flag.String("out", "assets", "输出目录")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}

	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	bySize := map[int]*image.RGBA{}
	for _, s := range sizes {
		bySize[s] = render(s)
	}

	icoPath := filepath.Join(*out, "icon.ico")
	if err := writeICO(icoPath, bySize, []int{128, 256}); err != nil {
		fatal(err)
	}
	pngPath := filepath.Join(*out, "icon.png")
	var pb bytes.Buffer
	if err := png.Encode(&pb, bySize[256]); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(pngPath, pb.Bytes(), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("已生成 %s、%s（尺寸 %v）\n", icoPath, pngPath, sizes)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "genicon:", err)
	os.Exit(1)
}
