// Package qr 二维码：解 get_qrcode 的 base64 PNG 拿内容，再渲染成紧凑的终端二维码。
//
// 终端字符格宽高约 1:2（高是宽的两倍），所以要让"模块"显示成正方形，
// 一个字符格必须横向放的模块数 : 纵向放的模块数 = 1:2：
//   - 半块 ▀▄█（1 模块宽 × 2 模块高/格）：模块正方、实心块，扫码最稳（默认）。
//   - 盲文点阵（2 模块宽 × 4 模块高/格）：模块仍正方、更小，但点阵有缝隙，很多扫码器读不了。
//
// 登录二维码内容是 333 字符的长 URL(≈65 模块)，实心块下宽度就得 ≈65 列，这是内容决定的、压不动。
// 默认半块；GOBOT_QR=braille 换盲文（更小，扫码器能读再用）。
package qr

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image/png"
	"os"
	"strings"

	"github.com/liyue201/goqr"
	"rsc.io/qr"
)

// DecodeQRPng 解出二维码内容（URL/字符串）。
func DecodeQRPng(pngBase64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(pngBase64)
	if err != nil {
		return "", err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	codes, err := goqr.Recognize(img)
	if err != nil {
		return "", err
	}
	if len(codes) == 0 {
		return "", errors.New("未识别到二维码")
	}
	return string(codes[0].Payload), nil
}

// RenderTerminal 内容 → 紧凑终端二维码（ECC=L）。默认半块（实心、可扫），GOBOT_QR=braille 换盲文。
func RenderTerminal(content string) (string, error) {
	code, err := qr.Encode(content, qr.L)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(os.Getenv("GOBOT_QR"), "braille") {
		return renderBraille(code), nil
	}
	return renderHalf(code), nil
}

// darkFn 返回一个判定：给定含静默区坐标是否为暗模块（越界/静默区=亮）。
func darkFn(code *qr.Code, quiet int) func(x, y int) bool {
	n := code.Size
	return func(x, y int) bool {
		x -= quiet
		y -= quiet
		if x < 0 || y < 0 || x >= n || y >= n {
			return false
		}
		return code.Black(x, y)
	}
}

// renderHalf 半块渲染：1 模块宽 × 2 模块高/字符，暗=实心。模块正方、实心，最稳。
func renderHalf(code *qr.Code) string {
	const quiet = 2
	dim := code.Size + quiet*2
	dark := darkFn(code, quiet)
	var b strings.Builder
	for y := 0; y < dim; y += 2 {
		for x := 0; x < dim; x++ {
			top, bot := dark(x, y), dark(x, y+1)
			switch {
			case top && bot:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bot:
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// renderBraille 盲文点阵：2 模块宽 × 4 模块高/字符（U+2800 起）。模块正方、尺寸最小。
// 盲文点位：dx∈{0,1}, dy∈{0..3} → bit
//
//	(0,0)=1  (1,0)=8
//	(0,1)=2  (1,1)=16
//	(0,2)=4  (1,2)=32
//	(0,3)=64 (1,3)=128
func renderBraille(code *qr.Code) string {
	const quiet = 3
	dim := code.Size + quiet*2
	dark := darkFn(code, quiet)
	var b strings.Builder
	for y := 0; y < dim; y += 4 {
		for x := 0; x < dim; x += 2 {
			var pat int
			if dark(x, y) {
				pat |= 0x01
			}
			if dark(x, y+1) {
				pat |= 0x02
			}
			if dark(x, y+2) {
				pat |= 0x04
			}
			if dark(x+1, y) {
				pat |= 0x08
			}
			if dark(x+1, y+1) {
				pat |= 0x10
			}
			if dark(x+1, y+2) {
				pat |= 0x20
			}
			if dark(x, y+3) {
				pat |= 0x40
			}
			if dark(x+1, y+3) {
				pat |= 0x80
			}
			if pat == 0 {
				b.WriteByte(' ') // 亮区用空格，保证静默区干净
			} else {
				b.WriteRune(rune(0x2800 + pat))
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// RenderPNG 把内容编码成 PNG 图片字节（含静默区，每模块 8px），用于存本地直接扫图。
func RenderPNG(content string) ([]byte, error) {
	code, err := qr.Encode(content, qr.L)
	if err != nil {
		return nil, err
	}
	code.Scale = 8
	return code.PNG(), nil
}

// PngToTerminalQR PNG(base64) → 终端二维码串。
func PngToTerminalQR(pngBase64 string) (string, error) {
	content, err := DecodeQRPng(pngBase64)
	if err != nil {
		return "", err
	}
	return RenderTerminal(content)
}
