// Package media IM 图片解码（1:1 逆自电脑版 player JS）。
// 图片下载回来是加密容器：iv(12) ‖ 密文 ‖ GCM_tag(16)，key = skey(64hex→32字节)，AES-256-GCM。
package media

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
)

// ImageResource 对应消息里的 resource_url。
type ImageResource struct {
	Skey          string   `json:"skey"`
	OriginURLList []string `json:"origin_url_list"`
	LargeURLList  []string `json:"large_url_list"`
	MediumURLList []string `json:"medium_url_list"`
	ThumbURLList  []string `json:"thumb_url_list"`
	MD5           string   `json:"md5"`
}

// DecryptImage 解密图片容器。
func DecryptImage(encrypted []byte, skeyHex string) ([]byte, error) {
	key, err := hex.DecodeString(skeyHex)
	if err != nil || len(key) != 32 {
		return nil, errors.New("skey 必须是 32 字节（64 位 hex）")
	}
	if len(encrypted) < 12+16 {
		return nil, errors.New("密文过短")
	}
	iv := encrypted[:12]
	rest := encrypted[12:] // 密文 + tag（Go GCM 约定 tag 附在末尾，与 WebCrypto 一致）
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, 12)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, iv, rest, nil)
}

// PickURL 挑一个可用图片 url（原图优先）。
func (r *ImageResource) PickURL() string {
	for _, l := range [][]string{r.OriginURLList, r.LargeURLList, r.MediumURLList, r.ThumbURLList} {
		if len(l) > 0 && l[0] != "" {
			return l[0]
		}
	}
	return ""
}

// SniffExt 从解出的字节推断扩展名。
func SniffExt(b []byte) string {
	switch {
	case len(b) >= 12 && string(b[8:12]) == "WEBP":
		return "webp"
	case len(b) >= 2 && b[0] == 0xff && b[1] == 0xd8:
		return "jpg"
	case len(b) >= 4 && b[0] == 0x89 && string(b[1:4]) == "PNG":
		return "png"
	case len(b) >= 8 && string(b[4:8]) == "ftyp":
		return "heic"
	default:
		return "img"
	}
}

func sniffMime(b []byte) string {
	switch SniffExt(b) {
	case "webp":
		return "image/webp"
	case "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "heic":
		return "image/heic"
	default:
		return "application/octet-stream"
	}
}

// FetchAndDecrypt 下载 url、用 skey 解密，返回图片字节。
func FetchAndDecrypt(url, skey string) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://www.douyin.com")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	enc, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return DecryptImage(enc, skey)
}
