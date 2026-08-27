package login

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gobot/internal/sign"
)

const nextURL = "https://www.douyin.com"

// LoginResult 登录结果。
type LoginResult struct {
	Cookie   string
	UID      string
	Name     string
	DeviceID string
}

// Hooks 登录过程回调。
type Hooks struct {
	// OnQrcode 收到二维码。qrContent 是服务端权威 URL（qrcode_index_url，直接编码即可，
	// 不要去解 PNG——goqr 解服务端 PNG 会串位）；pngBase64 是原始 PNG（仅当没有 URL 时兜底）。
	OnQrcode      func(qrContent, pngBase64, token string)
	OnScanned     func(screenName string)
	OnStatus      func(msg string)
	PromptSmsCode func(mobile string) string // 返回用户输入的短信码（明文）
}

// QRLogin 扫码登录。deviceID 为空则新生成。
func QRLogin(hooks Hooks, deviceID string) (*LoginResult, error) {
	if deviceID == "" {
		deviceID = GenDeviceID()
	}
	c := NewClient(deviceID, "")
	c.ttwidCheck()

	qr, err := c.call("/passport/web/get_qrcode/",
		map[string]string{"next": nextURL, "need_logo": "false", "need_short_url": "false"}, nil, false)
	if err != nil {
		return nil, err
	}
	d := jmap(qr["data"])
	qrcode := jstr(d["qrcode"])
	if qrcode == "" || jint(d["error_code"]) != 0 {
		return nil, fmt.Errorf("获取二维码失败: %v", qr)
	}
	token := jstr(d["token"])
	if hooks.OnQrcode != nil {
		hooks.OnQrcode(jstr(d["qrcode_index_url"]), qrcode, token)
	}

	expireAt := time.Now().Add(180 * time.Second)
	if et := jint(d["expire_time"]); et > 0 {
		expireAt = time.Unix(int64(et), 0)
	}
	baseBody := map[string]string{
		"need_logo": "false", "need_short_url": "false", "is_frontier": "true",
		"token": token, "is_new_login": "1", "next": nextURL,
	}
	extraBody := map[string]string{}
	scanned, mfaDone := false, false

	for time.Now().Before(expireAt) {
		body := map[string]string{}
		for k, v := range baseBody {
			body[k] = v
		}
		for k, v := range extraBody {
			body[k] = v
		}
		r, err := c.call("/passport/web/check_qrconnect/", nil, body, false)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		dd := jmap(r["data"])
		if !mfaDone && (jstr(dd["account_flow"]) == "verify" || dd["biz_params"] != nil) {
			if err := c.doMfa(dd, hooks); err != nil {
				return nil, err
			}
			extraBody = pickBizParams(jmap(dd["biz_params"]))
			mfaDone = true
			continue
		}
		switch jstr(dd["status"]) {
		case "scanned":
			if !scanned {
				scanned = true
				if hooks.OnScanned != nil {
					hooks.OnScanned(jstr(jmap(dd["scan_user_info"])["screen_name"]))
				}
			}
		case "confirmed":
			ud := jmap(dd["user_data"])
			name := jstr(ud["screen_name"])
			if name == "" {
				name = jstr(ud["name"])
			}
			if info, e := c.call("/passport/account/info/v2/", nil, nil, false); e == nil {
				id := jmap(info["data"])
				if n := jstr(id["screen_name"]); n != "" {
					name = n
				} else if n2 := jstr(id["name"]); n2 != "" {
					name = n2
				}
			}
			cookie := c.Jar.Header()
			if !strings.Contains(cookie, "sessionid") {
				return nil, errors.New("已确认但未拿到 sessionid")
			}
			uid := jnumStr(ud["user_id_str"])
			if uid == "" {
				uid = jnumStr(ud["user_id"])
			}
			return &LoginResult{Cookie: cookie, UID: uid, Name: name, DeviceID: deviceID}, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, errors.New("二维码已过期或超时，请重试")
}

func pickBizParams(bp map[string]any) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"passport_mfa_retry_tag", "std_verify_flow_id", "std_verify_scene",
		"std_verify_template", "std_verify_token", "std_verify_type", "std_verify_way"} {
		if v, ok := bp[k]; ok {
			out[k] = jstr(v)
		}
	}
	return out
}

func (c *Client) doMfa(d map[string]any, hooks Hooks) error {
	if hooks.PromptSmsCode == nil {
		return errors.New("触发短信二次验证，但未提供输入回调")
	}
	bp := jmap(d["biz_params"])
	cp := jmap(d["common_params"])
	pick := func(m map[string]any, k, def string) string {
		if v := jstr(m[k]); v != "" {
			return v
		}
		return def
	}
	mfa := map[string]string{
		"mix_mode": "1", "type": "3737", "encrypt_uid": jstr(d["encrypt_uid"]), "verify_ticket": "",
		"copywriting_key":          pick(cp, "copywriting_key", "qr_connect"),
		"ies_safety_diversion_tag": pick(cp, "ies_safety_diversion_tag", "mfa"),
		"new_verify_flow":          jstr(cp["new_verify_flow"]),
		"std_verify_flow_id":       pick(bp, "std_verify_flow_id", jstr(cp["std_verify_flow_id"])),
		"std_verify_scene":         pick(bp, "std_verify_scene", "account_login"),
		"std_verify_template":      pick(bp, "std_verify_template", "ato"),
		"std_verify_token":         pick(bp, "std_verify_token", jstr(cp["std_verify_token"])),
		"std_verify_type":          pick(bp, "std_verify_type", "MFA"),
		"std_verify_way":           "mobile_sms_verify",
	}
	withTail := func(extra map[string]string) map[string]string {
		b := map[string]string{}
		for k, v := range mfa {
			b[k] = v
		}
		for k, v := range extra {
			b[k] = v
		}
		b["aid"] = "339757"
		b["new_authn_sdk_version"] = "1.0.0.421-web"
		return b
	}

	sc, err := c.call("/passport/web/send_code/", nil, withTail(map[string]string{"is6Digits": "1"}), true)
	if err != nil {
		return err
	}
	mobile := jstr(jmap(sc["data"])["mobile"])
	if hooks.OnStatus != nil {
		hooks.OnStatus("已向 " + mobile + " 发送短信验证码")
	}
	code := strings.TrimSpace(hooks.PromptSmsCode(mobile))
	vc, err := c.call("/passport/web/validate_code/", nil, withTail(map[string]string{"code": sign.CodeEncrypt(code)}), true)
	if err != nil {
		return err
	}
	if jstr(jmap(vc["data"])["ticket"]) == "" {
		return fmt.Errorf("短信验证失败: %v", vc)
	}
	if hooks.OnStatus != nil {
		hooks.OnStatus("短信验证通过")
	}
	return nil
}
