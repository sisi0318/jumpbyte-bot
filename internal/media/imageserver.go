package media

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// 图片解密代理：不自己监听端口，挂到网关的 HTTP mux 上共用同一端口。
//
//	GET /img?u=<encodedUrl>&k=<skey> → 下载 + AES-256-GCM 解密 → 正常图片

var (
	proxyBase string // 网关地址，如 http://127.0.0.1:9503
	proxyMu   sync.RWMutex
)

// 只允许解密图床域名，堵住"取任意 URL"的 SSRF。
var allowedImageHosts = []string{
	"douyinpic.com", "douyin.com", "iesdouyin.com", "amemv.com",
	"byteimg.com", "ibyteimg.com", "bytedance.com", "pstatp.com",
}

func allowedImageHost(rawURL string) bool {
	pu, err := url.Parse(rawURL)
	if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") {
		return false
	}
	host := pu.Hostname()
	for _, s := range allowedImageHosts {
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

// SetProxyBase 网关启动时告知自己的地址，图片链接据此拼。
func SetProxyBase(base string) {
	proxyMu.Lock()
	proxyBase = strings.TrimRight(base, "/")
	proxyMu.Unlock()
}

// ImageHandler /img 处理器，挂到网关 mux 上。免鉴权（<img> 带不了 header）+ 域名白名单兜底。
func ImageHandler(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("u")
	k := r.URL.Query().Get("k")
	if u == "" || k == "" {
		http.Error(w, "need u & k", http.StatusBadRequest)
		return
	}
	if !allowedImageHost(u) {
		http.Error(w, "host not allowed", http.StatusForbidden)
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
}

// ImageLink 拼本地解密代理链接；未设 base（网关没起）时返回原始 url。
func ImageLink(rawURL, skey string) string {
	proxyMu.RLock()
	base := proxyBase
	proxyMu.RUnlock()
	if base == "" {
		return rawURL
	}
	return base + "/img?u=" + url.QueryEscape(rawURL) + "&k=" + url.QueryEscape(skey)
}
