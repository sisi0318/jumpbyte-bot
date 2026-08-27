package media

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
)

var (
	imgPort int
	imgMu   sync.Mutex
)

// StartImageServer 启动本地图片代理，返回端口（0=启动失败）。已启动则返回现有端口。
//
//	GET http://127.0.0.1:<port>/img?u=<encodedUrl>&k=<skey> → 下载+解密 → 正常图片
func StartImageServer(preferPort int) int {
	imgMu.Lock()
	defer imgMu.Unlock()
	if imgPort > 0 {
		return imgPort
	}
	if preferPort <= 0 {
		preferPort = 9520
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferPort))
	if err != nil {
		return 0
	}
	imgPort = preferPort
	mux := http.NewServeMux()
	mux.HandleFunc("/img", func(w http.ResponseWriter, r *http.Request) {
		u := r.URL.Query().Get("u")
		k := r.URL.Query().Get("k")
		if u == "" || k == "" {
			http.Error(w, "need u & k", http.StatusBadRequest)
			return
		}
		data, err := FetchAndDecrypt(u, k)
		if err != nil {
			http.Error(w, "err: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", sniffMime(data))
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(data)
	})
	go func() { _ = http.Serve(ln, mux) }()
	return imgPort
}

// ImageServerPort 当前端口（0=未启动）。
func ImageServerPort() int {
	imgMu.Lock()
	defer imgMu.Unlock()
	return imgPort
}

// ImageLink 拼一个本地解密代理链接；服务未起时返回原始 url。
func ImageLink(rawURL, skey string) string {
	p := ImageServerPort()
	if p <= 0 {
		return rawURL
	}
	return fmt.Sprintf("http://127.0.0.1:%d/img?u=%s&k=%s", p, url.QueryEscape(rawURL), url.QueryEscape(skey))
}
