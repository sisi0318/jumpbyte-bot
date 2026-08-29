package login

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"gobot/internal/abogus"
	"gobot/internal/sign"
)

const aid = "339757"
const defaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) douyinim/1.1.31 Chrome/130.0.6723.58 Electron/33.4.11 Safari/537.36"

func nowMs() int64 { return time.Now().UnixMilli() }

func randHex(n int) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, n)
	// 复用 msToken 的随机源足够；这里简单用时间+计数亦可，但直接 crypto 更稳
	src := []byte(sign.MsToken(n))
	for i := range b {
		b[i] = hexd[src[i]&0xf]
	}
	return string(b)
}

// CookieJar 极简 cookie jar。
type CookieJar struct{ store map[string]string }

func NewJar() *CookieJar { return &CookieJar{store: map[string]string{}} }

func (j *CookieJar) Load(s string) {
	for _, part := range strings.Split(s, ";") {
		kv := strings.TrimSpace(part)
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		j.store[kv[:i]] = kv[i+1:]
	}
}

func (j *CookieJar) Update(res *http.Response) {
	for _, c := range res.Cookies() {
		if c.Value == "" || strings.EqualFold(c.Value, "deleted") {
			delete(j.store, c.Name)
		} else {
			j.store[c.Name] = c.Value
		}
	}
}

func (j *CookieJar) Header() string {
	parts := make([]string, 0, len(j.store))
	for k, v := range j.store {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

func (j *CookieJar) Get(name string) string { return j.store[name] }

// Client passport web 请求器。
type Client struct {
	UA          string
	DeviceID    string
	fingerprint string
	Jar         *CookieJar
	hc          *http.Client
}

// NewClient deviceID 为空则新生成；cookie 非空则预置。
func NewClient(deviceID, cookie string) *Client {
	if deviceID == "" {
		deviceID = GenDeviceID()
	}
	c := &Client{
		UA:          defaultUA,
		DeviceID:    deviceID,
		fingerprint: buildFingerprint(deviceID),
		Jar:         NewJar(),
		hc:          &http.Client{Timeout: 30 * time.Second},
	}
	if cookie != "" {
		c.Jar.Load(cookie)
	}
	return c
}

func (c *Client) normalBase() map[string]string {
	return map[string]string{
		"passport_jssdk_version": "2.4.12", "passport_jssdk_type": "normal", "is_from_ttaccountsdk": "1",
		"aid": aid, "language": "zh", "ts": fmt.Sprint(time.Now().Unix()),
		"account_sdk_source": "web", "account_sdk_source_info": c.fingerprint,
		"p_js_v": "2.4.12", "p_js_t": "pro", "p_zt": "3.3.5", "p_ver": "1.0.29",
		"request_host": "file://", "p_bd": "1.0.1.7", "biz_trace_id": randHex(8),
		"device_id": c.DeviceID, "iid": "0", "version_code": "1.1.31", "device_platform": "PC",
		"is_from_iesaccountsaas": "1", "is_new_login": "1",
	}
}

func (c *Client) liteBase() map[string]string {
	return map[string]string{
		"passport_jssdk_version": "5.1.2", "passport_jssdk_type": "lite", "is_from_ttaccountsdk": "1",
		"aid": aid, "language": "zh", "account_app_language": "zh", "new_authn_sdk_version": "1.0.0.421-web",
		"biz_trace_id": randHex(8), "device_id": c.DeviceID, "iid": "0", "version_code": "1.1.31",
		"device_platform": "PC", "is_from_iesaccountsaas": "1", "is_new_login": "1",
	}
}

// paramCanonicalOrder 浏览器发包时的固定参数顺序（取自真机 HAR）。
// Go 的 map 迭代顺序随机，必须按此定序，否则每次请求参数顺序都变、
// a_bogus/服务端风控对不上，导致二维码请求异常。
var paramCanonicalOrder = map[string]int{
	"passport_jssdk_version": 0, "passport_jssdk_type": 1, "is_from_ttaccountsdk": 2,
	"aid": 3, "language": 4, "account_app_language": 5, "ts": 6,
	"next": 7, "need_logo": 8, "need_short_url": 9, "is_new_login": 10,
	"is_from_iesaccountsaas": 11, "account_sdk_source": 12, "account_sdk_source_info": 13,
	"p_js_v": 14, "p_js_t": 15, "p_zt": 16, "p_ver": 17, "request_host": 18, "p_bd": 19,
	"biz_trace_id": 20, "new_authn_sdk_version": 21,
	"device_id": 22, "iid": 23, "version_code": 24, "device_platform": 25,
	// 尾部签名参数固定压在最后：sign, qs, msToken, a_bogus
	"sign": 100, "qs": 101, "msToken": 102, "a_bogus": 103,
}

// orderKeys 按 canonical 顺序排序；未收录的 key 排在已知 key 之后、彼此按字典序。
func orderKeys(keys []string) {
	sort.Slice(keys, func(i, j int) bool {
		oi, iok := paramCanonicalOrder[keys[i]]
		oj, jok := paramCanonicalOrder[keys[j]]
		if iok && jok {
			return oi < oj
		}
		if iok != jok {
			return iok // 已知的排前面
		}
		return keys[i] < keys[j]
	})
}

func encodeKV(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	orderKeys(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + url.QueryEscape(m[k])
	}
	return strings.Join(parts, "&")
}

// call 发一个已签名的 passport 请求，返回解析后的 JSON。
func (c *Client) call(path string, queryExtra, body map[string]string, lite bool) (map[string]any, error) {
	base := c.normalBase()
	if lite {
		base = c.liteBase()
	}
	query := map[string]string{}
	for k, v := range base {
		query[k] = v
	}
	for k, v := range queryExtra {
		query[k] = v
	}
	if !lite {
		s, qs := sign.SignParams(query, body)
		query["sign"] = s
		query["qs"] = qs
	}
	query["msToken"] = sign.MsToken(128)

	bodyStr := encodeKV(body)
	queryStr := encodeKV(query)
	query["a_bogus"] = abogus.GetABogus(queryStr, bodyStr, c.UA, nowMs())
	fullURL := "https://imdesktop.douyin.com" + path + "?" + queryStr + "&a_bogus=" + url.QueryEscape(query["a_bogus"])

	method := "GET"
	var reqBody io.Reader
	if body != nil {
		method = "POST"
		reqBody = strings.NewReader(bodyStr)
	}
	req, err := http.NewRequest(method, fullURL, reqBody)
	if err != nil {
		return nil, err
	}
// bd-ticket-guard-iteration-version: 2
// bd-ticket-guard-ree-public-key: BEPhQJtcnGrFIlCf8/m+Boe2kyBwe7Wj0hKUVpdDZlj1Dbb4qkcqtSzxGD4eaO6mc4aG9alH1Ka95D1e1ngTKJg=
// bd-ticket-guard-server-cert-sn: 533240336124694022040808462028007165443034493949
// bd-ticket-guard-version: 2
// referer: https://imdesktop.douyin.com
// sec-ch-ua: "Not.A/Brand";v="99", "Chromium";v="136"
// sec-ch-ua-mobile: ?0
// sec-ch-ua-platform: "Windows"
// x-tt-passport-aid-sign: 437536ae85fd28413d036ecf7bf60798421979bdc1fcc15a493474d3bacfb525
// x-tt-passport-csrf-token: 
// x-tt-passport-trace-id: 81c3e95b
// x-tt-passport-verify-portrait: 41918735-2cb8-47d6-a412-9f970bb8410d.login
// priority: u=1, i
	req.Header.Set("bd-ticket-guard-version", "2")
	req.Header.Set("bd-ticket-guard-iteration-version", "2")
	req.Header.Set("bd-ticket-guard-ree-public-key", "BEPhQJtcnGrFIlCf8/m+Boe2kyBwe7Wj0hKUVpdDZlj1Dbb4qkcqtSzxGD4eaO6mc4aG9alH1Ka95D1e1ngTKJg=")
	req.Header.Set("bd-ticket-guard-server-cert-sn", "533240336124694022040808462028007165443034493949")
	req.Header.Set("x-tt-passport-aid-sign", "437536ae85fd28413d036ecf7bf60798421979bdc1fcc15a493474d3bacfb525")
	req.Header.Set("x-tt-passport-csrf-token", "")
	req.Header.Set("x-tt-passport-trace-id", "81c3e95b")
	req.Header.Set("x-tt-passport-verify-portrait", "41918735-2cb8-47d6-a412-9f970bb8410d.login")
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Referer", "https://imdesktop.douyin.com")
	// req.Header.Set("Origin", "https://imdesktop.douyin.com")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if ck := c.Jar.Header(); ck != "" {
		req.Header.Set("Cookie", ck)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	c.Jar.Update(res)
	data, _ := io.ReadAll(res.Body)
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{"_raw": string(data), "_status": res.StatusCode}, nil
	}
	return out, nil
}

// ttwidCheck 拿设备追踪 cookie（登录前置）。
func (c *Client) ttwidCheck() {
	payload, _ := json.Marshal(map[string]any{
		"aid": 339757, "service": "imdesktop.douyin.com", "unionHost": "https://ttwid.bytedance.com",
		"host": "https://imdesktop.douyin.com", "union": false, "needFid": false, "fid": "", "migrate_priority": 0,
	})
	req, _ := http.NewRequest("POST", "https://imdesktop.douyin.com/ttwid/check/", bytes.NewReader(payload))
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://imdesktop.douyin.com")
	// req.Header.Set("Origin", "https://imdesktop.douyin.com")
	if ck := c.Jar.Header(); ck != "" {
		req.Header.Set("Cookie", ck)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return
	}
	defer res.Body.Close()
	c.Jar.Update(res)
	_, _ = io.Copy(io.Discard, res.Body)
}
