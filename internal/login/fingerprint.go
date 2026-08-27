package login

import (
	"encoding/json"

	"gobot/internal/sign"
)

// buildFingerprint 生成 account_sdk_source_info（xor5(JSON)）。
// 内容是一份稳定合理的 Windows+Chromium 指纹；sign 会覆盖它，值本身服务端只要能解码即可。
func buildFingerprint(deviceID string) string {
	info := map[string]any{
		"hardwareConcurrency": 8,
		"webdriver":           false,
		"chromedriver":        false,
		"shelldriver":         false,
		"plugins":             5,
		"permissions":         []any{map[string]any{"name": "notifications", "state": "granted"}},
		"innerHeight":         484,
		"innerWidth":          726,
		"outerHeight":         484,
		"outerWidth":          726,
		"stoargeStatus": map[string]any{
			"indexedDB": map[string]any{
				"idb": "object", "open": "function", "indexedDB": "object",
				"IDBKeyRange": "function", "openDatabase": "function", "isSafari": false, "hasFetch": false,
			},
			"localStorage":       map[string]any{"isSupportLStorage": true, "size": 1993, "write": true},
			"storageQuotaStatus": map[string]any{"usage": 0, "quota": 36104626176, "isPrivate": false},
		},
		"webgl": map[string]any{
			"vendor":   "Google Inc. (Google)",
			"renderer": "ANGLE (Google, Vulkan 1.3.0 (SwiftShader Device (Subzero) (0x0000C0DE)), SwiftShader driver)",
		},
		"notificationPermission": "granted",
		"performance": map[string]any{
			"timeOrigin":     1787813991280.3,
			"usedJSHeapSize": 18200000,
			"navigationTiming": map[string]any{
				"decodedBodySize": 2527, "entryType": "navigation", "initiatorType": "navigation",
				"name":                 "file:///renderer/login/index.html?window=login&channel=0&guid=" + deviceID,
				"renderBlockingStatus": "non-blocking",
			},
		},
		"request_host":     "",
		"request_pathname": "/renderer/login/index.html",
		"browser":          map[string]any{"t": "7781993187871", "bit_protocol": "false", "bit_helper": false},
	}
	j, _ := json.Marshal(info)
	return sign.Xor5(string(j))
}
