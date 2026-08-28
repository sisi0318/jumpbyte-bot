package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const batchPlayInfoURL = "https://imdesktop.douyin.com/maya/story/batch_play_info/v1/"

// pcFingerprintQuery 电脑版 web 接口通用的设备指纹 query（config/v2、batch_play_info 共用，无 a_bogus）。
func (c *Client) pcFingerprintQuery() string {
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
	return sb.String()
}

// VideoURL 视频可播地址（batch_play_info 解出）。视频流本身仍是加密的（key=消息里的 video.skey）。
type VideoURL struct {
	MainURL    string `json:"main_url"`
	BackupURL  string `json:"backup_url"`
	ExpireTime int64  `json:"expire_time"`
}

// ResolveVideoURL 用视频 tkey 走 batch_play_info 换可播 URL（main/backup）。
func (c *Client) ResolveVideoURL(tkey string) (VideoURL, error) {
	var out VideoURL
	if strings.TrimSpace(tkey) == "" {
		return out, fmt.Errorf("空 tkey")
	}
	body, _ := json.Marshal(map[string]any{
		"req_infos":    []map[string]any{{"tos_key": tkey, "type": 2}},
		"with_caption": true,
	})
	req, _ := http.NewRequest("POST", batchPlayInfoURL+"?"+c.pcFingerprintQuery(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", pcUA)
	req.Header.Set("Cookie", c.Cookie)
	req.Header.Set("Referer", "https://imdesktop.douyin.com")
	resp, err := imHTTP.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var j struct {
		Data struct {
			PlayInfos []struct {
				EncryptedURL VideoURL `json:"encrypted_url"`
			} `json:"play_infos"`
		} `json:"data"`
	}
	if json.Unmarshal(rb, &j) != nil || len(j.Data.PlayInfos) == 0 {
		return out, fmt.Errorf("解析失败: %s", snippet(rb))
	}
	e := j.Data.PlayInfos[0].EncryptedURL
	if e.MainURL == "" {
		return out, fmt.Errorf("无播放地址: %s", snippet(rb))
	}
	return e, nil
}
