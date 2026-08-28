package media

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// 用真机加密样本走完整代理下载路径：TLS 假 CDN 提供 CENC MP4 → FetchVideoAndDecrypt → 明文可播 MP4。
// payload.mp4 在 har/（gitignore），缺失则跳过。
func TestFetchVideoAndDecrypt(t *testing.T) {
	const skey = "051d4c7b2d67fba130b4134745d15a6a"
	enc, err := os.ReadFile(filepath.Join("..", "..", "har", "payload.mp4"))
	if err != nil {
		t.Skip("无 har/payload.mp4")
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(enc)
	}))
	defer srv.Close()

	// 临时把下载客户端指向信任该测试证书的 client（同包可换）
	old := videoHTTP
	videoHTTP = srv.Client()
	defer func() { videoHTTP = old }()

	out, err := FetchVideoAndDecrypt(srv.URL, "", skey)
	if err != nil {
		t.Fatalf("下载解密失败: %v", err)
	}
	if len(out) != len(enc) {
		t.Fatalf("长度变化 %d → %d", len(enc), len(out))
	}
	if !bytes.Contains(out, []byte("hvc1")) || !bytes.Contains(out, []byte("mp4a")) {
		t.Fatal("未解出 hvc1/mp4a 明文入口")
	}
	if bytes.Contains(out, []byte("encv")) || bytes.Contains(out, []byte("senc")) {
		t.Fatal("仍残留 encv/senc")
	}
}

// backup 回退：main 报错时应改用 backup。
func TestFetchVideoBackupFallback(t *testing.T) {
	const skey = "051d4c7b2d67fba130b4134745d15a6a"
	enc, err := os.ReadFile(filepath.Join("..", "..", "har", "payload.mp4"))
	if err != nil {
		t.Skip("无 har/payload.mp4")
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(enc)
	}))
	defer srv.Close()
	old := videoHTTP
	videoHTTP = srv.Client()
	defer func() { videoHTTP = old }()

	// main 用非法 scheme 触发失败 → 回退 backup
	out, err := FetchVideoAndDecrypt("ftp://bad/main", srv.URL, skey)
	if err != nil {
		t.Fatalf("backup 回退失败: %v", err)
	}
	if !bytes.Contains(out, []byte("hvc1")) {
		t.Fatal("backup 未解出明文")
	}
}

func TestFetchVideoRejectsNonHTTPS(t *testing.T) {
	if _, err := FetchVideoAndDecrypt("http://x/main", "", "00"); err == nil {
		t.Fatal("http 应被拒")
	}
}

func TestVideoLink(t *testing.T) {
	SetProxyBase("http://127.0.0.1:9503/")
	got := VideoLink("tos-cn-o/abc", "051d4c7b2d67fba130b4134745d15a6a")
	want := "http://127.0.0.1:9503/video?tkey=tos-cn-o%2Fabc&skey=051d4c7b2d67fba130b4134745d15a6a"
	if got != want {
		t.Fatalf("VideoLink=%q want %q", got, want)
	}
	SetProxyBase("")
	if VideoLink("t", "k") != "" {
		t.Fatal("无 base 应返回空串")
	}
	SetProxyBase("http://127.0.0.1:9503")
	if VideoLink("", "k") != "" || VideoLink("t", "") != "" {
		t.Fatal("缺 tkey/skey 应返回空串")
	}
}
