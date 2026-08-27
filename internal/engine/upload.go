package engine

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/google/uuid"
)

// 图片上传：走电脑版的 TOS/VOD 上传链（标准 AWS SigV4 签名），拿到发图用的 ImageAsset。
// 逆自真机 HAR：config/v2(拿 STS 凭证) → ApplyUploadInner(SigV4) → TOS PUT → CommitUploadInner(SigV4)。
const (
	uploadConfigURL = "https://www.douyin.com/aweme/v1/web/im/upload/config/v2"
	vodBase         = "https://vod.bytedanceapi.com/"
	vodRegion       = "cn-north-1"
	vodService      = "vod"
)

type stsCreds struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	SpaceName       string
}

// UploadImage 上传图片字节，返回可直接发送的 ImageAsset。
func (c *Client) UploadImage(imageBytes []byte) (ImageAsset, error) {
	var out ImageAsset
	if len(imageBytes) == 0 {
		return out, fmt.Errorf("空图片")
	}
	creds, err := c.getUploadConfig()
	if err != nil {
		return out, fmt.Errorf("拿上传凭证失败: %w", err)
	}

	size := len(imageBytes)
	crc := fmt.Sprintf("%08x", crc32.ChecksumIEEE(imageBytes))
	w, h := 0, 0
	if cfg, _, e := image.DecodeConfig(bytes.NewReader(imageBytes)); e == nil {
		w, h = cfg.Width, cfg.Height
	}

	storeURI, auth, host, sessionKey, err := c.applyUpload(creds, creds.SpaceName, "image", size)
	if err != nil {
		return out, fmt.Errorf("ApplyUpload 失败: %w", err)
	}
	if err := c.tosPut(host, storeURI, auth, crc, imageBytes); err != nil {
		return out, fmt.Errorf("TOS 上传失败: %w", err)
	}
	oid, skey, md5s, err := c.commitUpload(creds, sessionKey)
	if err != nil {
		return out, fmt.Errorf("CommitUpload 失败: %w", err)
	}
	return ImageAsset{Oid: oid, Skey: skey, Md5: md5s, DataSize: size, CoverWidth: w, CoverHeight: h}, nil
}

// getUploadConfig GET config/v2 拿 STS 凭证 + space_name（cookie 鉴权，无 a_bogus）。
func (c *Client) getUploadConfig() (stsCreds, error) {
	var cr stsCreds
	dev := c.deviceID()
	q := [][2]string{
		{"aid", "339757"}, {"version_name", "1.1.33"}, {"version_code", "1.1.33"},
		{"device_platform", "win32"}, {"os_version", "10.0.26200"},
		{"screen_width", "1707"}, {"screen_height", "1067"},
		{"browser_language", "zh-CN"}, {"browser_platform", "Win32"}, {"browser_name", "Mozilla"},
		{"browser_version", strings.TrimPrefix(pcUA, "Mozilla/")}, {"browser_online", "true"}, {"cookie_enabled", "true"},
		{"device_id", dev}, {"did", dev}, {"iid", "0"},
		{"awemeim_guid", strings.ReplaceAll(uuid.NewString(), "-", "")}, {"channel", "0"},
	}
	var sb strings.Builder
	for i, kv := range q {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(kv[0] + "=" + rawURLEncode(kv[1]))
	}
	req, _ := http.NewRequest("GET", uploadConfigURL+"?"+sb.String(), nil)
	req.Header.Set("User-Agent", pcUA)
	req.Header.Set("Cookie", c.Cookie)
	req.Header.Set("Referer", "https://www.douyin.com/")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return cr, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var j struct {
		PIC struct {
			AccessKeyID     string `json:"access_key_id"`
			SecretAccessKey string `json:"secret_access_key"`
			SessionToken    string `json:"session_token"`
			SpaceName       string `json:"space_name"`
		} `json:"public_image_config"`
	}
	if err := json.Unmarshal(rb, &j); err != nil {
		return cr, fmt.Errorf("解析失败: %s", snippet(rb))
	}
	if j.PIC.AccessKeyID == "" || j.PIC.SecretAccessKey == "" {
		return cr, fmt.Errorf("无 STS 凭证（cookie 失效?）: %s", snippet(rb))
	}
	return stsCreds{j.PIC.AccessKeyID, j.PIC.SecretAccessKey, j.PIC.SessionToken, j.PIC.SpaceName}, nil
}

// applyUpload GET ApplyUploadInner（SigV4）→ storeURI, auth(JWT), uploadHost, sessionKey。
// 图片响应在 Result.UploadAddress；视频（分片）在 Result.InnerUploadAddress.UploadNodes[0]。
func (c *Client) applyUpload(cr stsCreds, space, fileType string, size int) (storeURI, auth, host, sessionKey string, err error) {
	query := map[string]string{
		"Action": "ApplyUploadInner", "Version": "2020-11-19", "SpaceName": space,
		"FileType": fileType, "IsInner": "1", "NeedFallback": "true",
		"FileSize": strconv.Itoa(size), "s": randLower(11),
	}
	req := vodSignedRequest("GET", query, nil, cr)
	resp, e := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if e != nil {
		return "", "", "", "", e
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	type storeInfo struct {
		StoreUri string `json:"StoreUri"`
		Auth     string `json:"Auth"`
	}
	var j struct {
		Result struct {
			UploadAddress *struct {
				StoreInfos  []storeInfo `json:"StoreInfos"`
				UploadHosts []string    `json:"UploadHosts"`
				SessionKey  string      `json:"SessionKey"`
			} `json:"UploadAddress"`
			InnerUploadAddress *struct {
				UploadNodes []struct {
					StoreInfos []storeInfo `json:"StoreInfos"`
					UploadHost string      `json:"UploadHost"`
					SessionKey string      `json:"SessionKey"`
				} `json:"UploadNodes"`
			} `json:"InnerUploadAddress"`
		} `json:"Result"`
	}
	if json.Unmarshal(rb, &j) != nil {
		return "", "", "", "", fmt.Errorf("响应异常: %s", snippet(rb))
	}
	if ua := j.Result.UploadAddress; ua != nil && len(ua.StoreInfos) > 0 && len(ua.UploadHosts) > 0 {
		return ua.StoreInfos[0].StoreUri, ua.StoreInfos[0].Auth, ua.UploadHosts[0], ua.SessionKey, nil
	}
	if iu := j.Result.InnerUploadAddress; iu != nil && len(iu.UploadNodes) > 0 && len(iu.UploadNodes[0].StoreInfos) > 0 {
		n := iu.UploadNodes[0]
		return n.StoreInfos[0].StoreUri, n.StoreInfos[0].Auth, n.UploadHost, n.SessionKey, nil
	}
	return "", "", "", "", fmt.Errorf("无上传地址: %s", snippet(rb))
}

// tosPut 把图片字节 PUT/POST 到 TOS。
func (c *Client) tosPut(host, storeURI, auth, crc string, data []byte) error {
	url := "https://" + host + "/upload/v1/" + storeURI
	req, _ := http.NewRequest("POST", url, bytes.NewReader(data))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-CRC32", crc)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Storage-U", c.CkUid)
	req.Header.Set("User-Agent", pcUA)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var j struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(rb, &j) != nil || j.Code != 2000 {
		return fmt.Errorf("TOS 返回 %s", snippet(rb))
	}
	return nil
}

// commitUpload POST CommitUploadInner（SigV4）→ oid(Encryption.Uri), skey, md5。
func (c *Client) commitUpload(cr stsCreds, sessionKey string) (oid, skey, md5s string, err error) {
	query := map[string]string{"Action": "CommitUploadInner", "Version": "2020-11-19", "SpaceName": cr.SpaceName}
	body, _ := json.Marshal(map[string]string{"SessionKey": sessionKey})
	req := vodSignedRequest("POST", query, body, cr)
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	resp, e := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if e != nil {
		return "", "", "", e
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var j struct {
		Result struct {
			Results []struct {
				Encryption struct {
					Uri       string `json:"Uri"`
					SecretKey string `json:"SecretKey"`
					SourceMd5 string `json:"SourceMd5"`
				} `json:"Encryption"`
			} `json:"Results"`
		} `json:"Result"`
	}
	if json.Unmarshal(rb, &j) != nil || len(j.Result.Results) == 0 {
		return "", "", "", fmt.Errorf("响应异常: %s", snippet(rb))
	}
	en := j.Result.Results[0].Encryption
	if en.Uri == "" || en.SecretKey == "" {
		return "", "", "", fmt.Errorf("无加密信息: %s", snippet(rb))
	}
	return en.Uri, en.SecretKey, en.SourceMd5, nil
}

// -- AWS SigV4 --------------------------------------------------------------

// vodSignedRequest 构造一个 SigV4 已签名的 vod 请求（body 为 nil 表示 GET/空体）。
func vodSignedRequest(method string, query map[string]string, body []byte, cr stsCreds) *http.Request {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	cq := canonicalQuery(query)
	auth, signed := vodSign(method, cq, body, cr, amzDate, dateStamp)

	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(method, vodBase+"?"+cq, rd)
	req.Header.Set("Authorization", auth)
	for k, v := range signed {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", pcUA)
	return req
}

// vodSign 纯计算：返回 Authorization 头 + 需随请求发送的签名头（可注入时间戳，便于对拍）。
func vodSign(method, canonicalQ string, body []byte, cr stsCreds, amzDate, dateStamp string) (string, map[string]string) {
	payloadHash := sha256Hex(body)
	signed := map[string]string{
		"x-amz-date":           amzDate,
		"x-amz-security-token": cr.SessionToken,
	}
	if method == "POST" {
		signed["x-amz-content-sha256"] = payloadHash
	}
	hk := make([]string, 0, len(signed))
	for k := range signed {
		hk = append(hk, k)
	}
	sort.Strings(hk)
	var ch strings.Builder
	for _, k := range hk {
		ch.WriteString(k + ":" + signed[k] + "\n")
	}
	signedHeaders := strings.Join(hk, ";")

	canonicalReq := method + "\n/\n" + canonicalQ + "\n" + ch.String() + "\n" + signedHeaders + "\n" + payloadHash
	scope := dateStamp + "/" + vodRegion + "/" + vodService + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalReq))

	key := hmacSum([]byte("AWS4"+cr.SecretAccessKey), dateStamp)
	key = hmacSum(key, vodRegion)
	key = hmacSum(key, vodService)
	key = hmacSum(key, "aws4_request")
	sig := hex.EncodeToString(hmacRaw(key, stringToSign))

	auth := "AWS4-HMAC-SHA256 Credential=" + cr.AccessKeyID + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + sig
	return auth, signed
}

// canonicalQuery 键排序后 RFC3986 编码拼接（AWS 规则）。
func canonicalQuery(q map[string]string) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = rawURLEncode(k) + "=" + rawURLEncode(q[k])
	}
	return strings.Join(parts, "&")
}

func sha256Hex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func hmacRaw(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}
func hmacSum(key []byte, data string) []byte { return hmacRaw(key, data) }

// randLower n 位小写字母数字（cache-buster 用）。
func randLower(n int) string {
	const al = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	src := uuid.NewString() + uuid.NewString()
	for i := 0; i < n; i++ {
		b[i] = al[int(src[i])%len(al)]
	}
	return string(b)
}
