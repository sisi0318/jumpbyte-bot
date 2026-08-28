package media

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// 视频解密：CENC 视频流本身仍是密文，key = 消息里的 video.skey。
// 播放链接走网关 /video 代理：tkey 换下载地址 → 下载 → DecryptVideo → 出明文 MP4。
// 下载地址由服务端用 tkey 换得（非调用方给的任意 URL），故无 SSRF 面；仅校验 https。

const maxVideoBytes = 512 << 20 // 512MB 兜底，IM 短视频远小于此

var videoHTTP = &http.Client{Timeout: 90 * time.Second}

// FetchVideoAndDecrypt 下载 CENC MP4（main 失败回退 backup）并用 skey 解密成可播 MP4。
func FetchVideoAndDecrypt(mainURL, backupURL, skey string) ([]byte, error) {
	enc, err := fetchVideo(mainURL)
	if err != nil && backupURL != "" {
		enc, err = fetchVideo(backupURL)
	}
	if err != nil {
		return nil, err
	}
	return DecryptVideo(enc, skey)
}

func fetchVideo(rawURL string) ([]byte, error) {
	pu, err := url.Parse(rawURL)
	if err != nil || pu.Scheme != "https" {
		return nil, fmt.Errorf("非法视频地址")
	}
	req, _ := http.NewRequest("GET", rawURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://www.douyin.com")
	res, err := videoHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载视频 HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, maxVideoBytes))
}

// VideoLink 拼本地视频解密代理链接；未设 base（网关没起）时返回空串。
func VideoLink(tkey, skey string) string {
	proxyMu.RLock()
	base := proxyBase
	proxyMu.RUnlock()
	if base == "" || tkey == "" || skey == "" {
		return ""
	}
	return base + "/video?tkey=" + url.QueryEscape(tkey) + "&skey=" + url.QueryEscape(skey)
}
