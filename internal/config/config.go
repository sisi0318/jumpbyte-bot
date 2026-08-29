// Package config cookie.json 读写（单层扁平对象，强制单账号）。
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Dir cookie.json / bot.db 所在目录，默认当前工作目录（分发时就近放）。
var Dir = "."

// Account 单账号配置。
type Account struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Cookie   string `json:"cookie"`
	Phone    string `json:"phone"` // 填了且 cookie 为空/失效时，改用短信验证码登录
	UID      string `json:"uid"`
	DeviceID string `json:"device_id"`
	Proxy    string `json:"proxy"`
	ProxyAPI string `json:"proxy_api"`
	Channel  int    `json:"channel"`
	Enabled  bool   `json:"enabled"`
}

// CookiePath cookie.json 路径。
func CookiePath() string { return filepath.Join(Dir, "cookie.json") }

// DBPath sqlite 路径。
func DBPath() string { return filepath.Join(Dir, "bot.db") }

func normalize(a *Account) {
	if a.ID == "" {
		a.ID = "main"
	}
	if a.Channel == 0 {
		a.Channel = 1
	}
}

// LoadAccount 读单账号；支持单层对象 / 数组(>1报错) / 纯 cookie 字符串。
func LoadAccount() (*Account, error) {
	data, err := os.ReadFile(CookiePath())
	if err != nil {
		return nil, err
	}
	// 1) 单层对象（cookie 或 phone 至少有一个：phone-only 表示走短信登录）
	var obj Account
	if json.Unmarshal(data, &obj) == nil && (obj.Cookie != "" || obj.Phone != "") {
		obj.Enabled = enabledDefault(data)
		normalize(&obj)
		return &obj, nil
	}
	// 2) 数组
	var arr []Account
	if json.Unmarshal(data, &arr) == nil && len(arr) > 0 {
		if len(arr) > 1 {
			return nil, errors.New("本 bot 仅支持单账号")
		}
		a := arr[0]
		normalize(&a)
		if a.Cookie == "" && a.Phone == "" {
			return nil, errors.New("cookie.json 里既没有 cookie 也没有 phone")
		}
		return &a, nil
	}
	// 3) 纯 cookie 字符串
	var s string
	if json.Unmarshal(data, &s) == nil && s != "" {
		a := Account{Cookie: s, Enabled: true}
		normalize(&a)
		return &a, nil
	}
	return nil, errors.New("cookie.json 应为单层对象、数组或字符串")
}

// enabledDefault：对象里没写 enabled 时默认 true（Go 零值是 false）。
func enabledDefault(data []byte) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) == nil {
		if v, ok := m["enabled"]; ok {
			var b bool
			if json.Unmarshal(v, &b) == nil {
				return b
			}
		}
	}
	return true
}

// SaveAccount 单账号覆盖写（扁平对象）。
func SaveAccount(a *Account) error {
	normalize(a)
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(CookiePath(), append(data, '\n'), 0o644)
}
