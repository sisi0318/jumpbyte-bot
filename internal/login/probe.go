package login

// ProbeResult cookie 探测结果。
type ProbeResult struct {
	Alive   bool // 登录态有效
	Expired bool // 明确会话过期（可据此唤起登录）；网络错误不算
	UID     string
	Name    string
	Reason  string
}

// ProbeCookie GET /passport/account/info/v2/：user_id>0 && error_code==0 为活。
func ProbeCookie(cookie, deviceID string) ProbeResult {
	c := NewClient(deviceID, cookie)
	r, err := c.call("/passport/account/info/v2/", nil, nil, false)
	if err != nil {
		return ProbeResult{Reason: "网络错误：" + err.Error()}
	}
	d := jmap(r["data"])
	uid := jnumStr(d["user_id"])
	if uid != "" && uid != "0" && jint(d["error_code"]) == 0 {
		name := jstr(d["screen_name"])
		if name == "" {
			name = jstr(d["name"])
		}
		return ProbeResult{Alive: true, UID: uid, Name: name, Reason: "ok"}
	}
	reason := jstr(d["description"])
	if reason == "" {
		reason = jstr(r["message"])
	}
	if reason == "" {
		reason = "expired"
	}
	return ProbeResult{Expired: true, Reason: reason}
}
