package qr

import (
	"image"
	"image/color"
	"os"
	"strings"
	"testing"

	"github.com/liyue201/goqr"
	"rsc.io/qr"
)

// renderMatrixImage 把二维码矩阵画成放大的黑白图（暗模块=黑，含 4 模块静默区）。
func renderMatrixImage(t *testing.T, content string, scale int) image.Image {
	t.Helper()
	code, err := qr.Encode(content, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	n := code.Size
	quiet := 4
	dim := (n + quiet*2) * scale
	img := image.NewGray(image.Rect(0, 0, dim, dim))
	for y := 0; y < dim; y++ {
		for x := 0; x < dim; x++ {
			mx, my := x/scale-quiet, y/scale-quiet
			c := color.Gray{Y: 255}
			if mx >= 0 && my >= 0 && mx < n && my < n && code.Black(mx, my) {
				c = color.Gray{Y: 0}
			}
			img.SetGray(x, y, c)
		}
	}
	return img
}

func goqrRecognize(img image.Image) ([][]byte, error) {
	codes, err := goqr.Recognize(img)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, len(codes))
	for i, c := range codes {
		out[i] = c.Payload
	}
	return out, nil
}

func TestDecodeRealDouyinQR(t *testing.T) {
	b, err := os.ReadFile("testdata/qr.b64")
	if err != nil {
		t.Skip("无 testdata/qr.b64")
	}
	content, err := DecodeQRPng(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "token=") || !strings.Contains(content, "scan") {
		t.Fatalf("解出的内容不像扫码 URL: %s", content)
	}
	dims := func(s string) (int, int) {
		lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
		cols := 0
		for _, l := range lines {
			if w := len([]rune(l)); w > cols {
				cols = w
			}
		}
		return len(lines), cols
	}

	half, err := RenderTerminal(content) // 默认半块
	if err != nil {
		t.Fatal(err)
	}
	hr, hc := dims(half)
	t.Logf("半块默认: %d行×%d列", hr, hc)
	// 半块每模块 1 列，应约等于模块数，不该翻倍（翻倍=用了 2 字符/模块 = 太宽）
	if hc > 75 {
		t.Errorf("半块二维码过宽: %d列", hc)
	}

	t.Setenv("GOBOT_QR", "braille")
	braille, err := RenderTerminal(content)
	if err != nil {
		t.Fatal(err)
	}
	br, bc := dims(braille)
	t.Logf("盲文可选: %d行×%d列", br, bc)
	if br > 24 || bc > 40 {
		t.Errorf("盲文二维码过大: %d行×%d列", br, bc)
	}
}

// TestRenderMatrixScannable 校验渲染用的模块矩阵极性正确：
// 用 rsc.io/qr 生成矩阵 → 画成黑白图 → 用 goqr 解回，应等于原内容。
func TestRenderMatrixScannable(t *testing.T) {
	// 用真实登录 URL（含 qr_source_aid=339757、333 字符）走一遍 编码→高清图→解码，
	// 确认我们自己渲染的二维码不会串位（服务端 PNG 被 goqr 串位是另一回事，已改为直接编码权威 URL）。
	content := "https://api.amemv.com/ucenter_web/app/aweme/scan_login/index/douyin_scan_code_login/cn/app/index.html?_pia_=1&device_platform=PC&hide_nav_bar=1&is_new_login=1&loader_name=forest&next_url=https%3A%2F%2Fapi.amemv.com%2Fpassport%2Fmobile%2Fscan_qrcode%2F&qr_source_aid=339757&token=1e4da96e8e2adf594cfebf7c90299858_lq&web_app_from=rcode"
	img := renderMatrixImage(t, content, 6)
	codes, err := goqrRecognize(img)
	if err != nil {
		t.Fatalf("goqr 解码失败: %v", err)
	}
	if len(codes) == 0 || string(codes[0]) != content {
		t.Fatalf("解回内容不符:\n want=%s\n got =%q", content, codes)
	}
}
