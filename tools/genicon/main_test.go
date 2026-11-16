package main

import (
	"image/color"
	"testing"
)

func TestHexRGB(t *testing.T) {
	c := hexRGB("#2d3035")
	if c != [3]float64{45, 48, 53} {
		t.Errorf("hexRGB(\"#2d3035\") = %v，期望 [45 48 53]", c)
	}
}

func TestRenderCenterIsDark(t *testing.T) {
	img := render(32)
	// 画面中心在钥匙环外的空白处：应为深灰背景而非白色
	c := color.RGBAModel.Convert(img.At(26, 8)).(color.RGBA)
	if c.R > 90 || c.G > 90 || c.B > 90 {
		t.Errorf("背景应为深灰，得到 %+v", c)
	}
	if c.A != 255 {
		t.Errorf("背景应不透明，得到 alpha=%d", c.A)
	}
}

func TestRenderCornersTransparent(t *testing.T) {
	img := render(32)
	if img.Pix[3] != 0 { // 左上角像素（方块圆角外）
		t.Errorf("圆角外应为透明，得到 %+v", img.At(0, 0))
	}
}
