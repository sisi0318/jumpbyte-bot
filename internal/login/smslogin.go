package login

import (
	"errors"
	"fmt"
	"strings"

	"gobot/internal/sign"
)

// SMSLogin 手机号 + 短信验证码登录。deviceID 为空则新生成。
//
// mobile/code 都用 code_encrypt（Xor5+hex，见 sign.CodeEncrypt）加密；其余签名参数
// （sign/qs/msToken/a_bogus/account_sdk_source_info）与扫码登录同源，都由 c.call 统一生成，
// 请求头也与扫码登录一致。流程：send_code 发码 → 回调取码 → sms_login 换 cookie。
func SMSLogin(mobile string, hooks Hooks, deviceID string) (*LoginResult, error) {
	m := formatMobile(mobile)
	if m == "" {
		return nil, errors.New("手机号为空")
	}
	if hooks.PromptSmsCode == nil {
		return nil, errors.New("未提供短信验证码输入回调")
	}
	if deviceID == "" {
		deviceID = GenDeviceID()
	}
	c := NewClient(deviceID, "")
	c.ttwidCheck()

	encMobile := sign.CodeEncrypt(m)

	// 1) 发送验证码：type=24（手机验证码登录场景），is6Digits=1 六位码
	sc, err := c.call("/passport/web/send_code/", nil, map[string]string{
		"mix_mode":       "1",
		"mobile":         encMobile,
		"type":           sign.CodeEncrypt("24"),
		"is6Digits":      "1",
		"fixed_mix_mode": "1",
	}, false)
	if err != nil {
		return nil, err
	}
	if jstr(sc["message"]) != "success" {
		return nil, fmt.Errorf("发送验证码失败：%s", errDesc(sc))
	}
	shown := jstr(jmap(sc["data"])["mobile"]) // 服务端回的打码号，如 192******23
	if shown == "" {
		shown = m
	}
	if hooks.OnStatus != nil {
		hooks.OnStatus("已向 " + shown + " 发送短信验证码")
	}

	code := strings.TrimSpace(hooks.PromptSmsCode(shown))
	if code == "" {
		return nil, errors.New("验证码为空")
	}

	// 2) 验证码登录：成功后响应 Set-Cookie 带 sessionid
	lg, err := c.call("/passport/web/sms_login/", nil, map[string]string{
		"service":        nextURL,
		"mix_mode":       "1",
		"mobile":         encMobile,
		"code":           sign.CodeEncrypt(code),
		"fixed_mix_mode": "1",
		"login_only":     "true",
	}, false)
	if err != nil {
		return nil, err
	}
	if jstr(lg["message"]) != "success" {
		return nil, fmt.Errorf("验证码登录失败：%s", errDesc(lg))
	}
	cookie := c.Jar.Header()
	if !strings.Contains(cookie, "sessionid") {
		return nil, errors.New("登录成功但未拿到 sessionid")
	}
	ud := jmap(lg["data"])
	uid := jnumStr(ud["user_id_str"])
	if uid == "" {
		uid = jnumStr(ud["user_id"])
	}
	name := jstr(ud["name"])
	if sn := jstr(ud["screen_name"]); sn != "" {
		name = sn
	}
	return &LoginResult{Cookie: cookie, UID: uid, Name: name, DeviceID: deviceID}, nil
}

// formatMobile 归一成 "+86 <号码>"（服务端要求国家码与号码间有一个空格）。
// 已带 +86 的规整空格；其它 + 开头的国家码原样返回（调用方自证格式）。
func formatMobile(raw string) string {
	raw = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(raw))
	switch {
	case raw == "":
		return ""
	case strings.HasPrefix(raw, "+86"):
		return "+86 " + raw[3:]
	case strings.HasPrefix(raw, "+"):
		return raw
	case strings.HasPrefix(raw, "86") && len(raw) > 11:
		return "+86 " + raw[2:]
	default:
		return "+86 " + raw
	}
}

// errDesc 从 passport 响应里抽出可读错误信息。
func errDesc(r map[string]any) string {
	d := jmap(r["data"])
	if s := jstr(d["description"]); s != "" {
		return s
	}
	if s := jstr(d["error_str"]); s != "" {
		return s
	}
	if s := jstr(r["message"]); s != "" {
		return s
	}
	return fmt.Sprintf("%v", r)
}
