// Package webapi 电脑版 web IM 辅助接口（a_bogus+msToken+cookie 签名）。
package webapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gobot/internal/abogus"
	"gobot/internal/sign"
	"gobot/internal/store"
)

const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) douyinim/1.1.31 Chrome/130.0.6723.58 Electron/33.4.11 Safari/537.36"

// User 解析出的用户。
type User struct{ UID, SecUID, Nickname, Avatar string }

func encodeKV(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+url.QueryEscape(v))
	}
	return strings.Join(parts, "&")
}

// FetchUserInfo 直接请求 im/user/info（multipart sec_user_ids）。失败返回空表。
func FetchUserInfo(cookie string, secUids []string, deviceID string) map[string]User {
	out := map[string]User{}
	if len(secUids) == 0 {
		return out
	}
	if deviceID == "" {
		deviceID = "0"
	}
	q := map[string]string{
		"aid": "339757", "device_platform": "webapp", "version_code": "1.1.31", "version_name": "1.1.31",
		"device_id": deviceID, "channel": "channel_pc_web", "msToken": sign.MsToken(128),
	}
	queryStr := encodeKV(q)
	boundary := "----botFormBoundary" + sign.MsToken(16)
	secJSON, _ := json.Marshal(secUids)
	body := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"sec_user_ids\"\r\n\r\n" +
		string(secJSON) + "\r\n--" + boundary + "--\r\n"
	ab := abogus.GetABogus(queryStr, body, ua, time.Now().UnixMilli())
	u := "https://imdesktop.douyin.com/aweme/v1/web/im/user/info/?" + queryStr + "&a_bogus=" + url.QueryEscape(ab)

	req, err := http.NewRequest("POST", u, strings.NewReader(body))
	if err != nil {
		return out
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Referer", "https://imdesktop.douyin.com")
	req.Header.Set("Origin", "https://imdesktop.douyin.com")
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return out
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	var j struct {
		Data []struct {
			UID         any    `json:"uid"`
			SecUID      string `json:"sec_uid"`
			Nickname    string `json:"nickname"`
			AvatarThumb struct {
				URLList []string `json:"url_list"`
			} `json:"avatar_thumb"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &j) != nil {
		return out
	}
	for _, d := range j.Data {
		if d.SecUID == "" {
			continue
		}
		avatar := ""
		if len(d.AvatarThumb.URLList) > 0 {
			avatar = d.AvatarThumb.URLList[0]
		}
		out[d.SecUID] = User{UID: uidStr(d.UID), SecUID: d.SecUID, Nickname: d.Nickname, Avatar: avatar}
	}
	return out
}

func uidStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	}
	return ""
}

// ResolveUsers 带 sqlite 缓存：先查缓存，缺的才请求并写回。
func ResolveUsers(cookie string, secUids []string, deviceID string) map[string]User {
	out := map[string]User{}
	seen := map[string]bool{}
	var uniq []string
	for _, s := range secUids {
		if s != "" && !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	if len(uniq) == 0 {
		return out
	}
	for k, v := range store.GetCachedUsers(uniq) {
		out[k] = User{UID: v.UID, SecUID: v.SecUID, Nickname: v.Nickname, Avatar: v.Avatar}
	}
	var missing []string
	for _, s := range uniq {
		if _, ok := out[s]; !ok {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		fetched := FetchUserInfo(cookie, missing, deviceID)
		var toCache []store.CachedUser
		for k, v := range fetched {
			out[k] = v
			toCache = append(toCache, store.CachedUser{SecUID: v.SecUID, UID: v.UID, Nickname: v.Nickname, Avatar: v.Avatar})
		}
		store.PutUsers(toCache)
	}
	return out
}
