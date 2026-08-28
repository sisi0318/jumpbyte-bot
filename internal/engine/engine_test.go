package engine

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, raw string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		t.Fatalf("json 解析失败: %v", err)
	}
}

func md5Ref(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// protobuf 编解码往返：KV pair。
func TestKvPairRoundTrip(t *testing.T) {
	buf := encodeKvPair(5, "msg_type", "cmd100")
	fields := decodeTop(buf)
	if len(fields) != 1 || fields[0].Field != 5 || fields[0].Type != "message" {
		t.Fatalf("外层解码不对: %+v", fields)
	}
	inner := fields[0].VMsg
	if len(inner) != 2 || inner[0].VStr != "msg_type" || inner[1].VStr != "cmd100" {
		t.Fatalf("KV 解码不对: %+v", inner)
	}
}

// varint 往返（含大 ID）。
func TestVarintRoundTrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 127, 128, 300, 16384, 7513294668710805565} {
		buf := encodeVarint(v)
		got, _, ok := decodeVarintAt(buf, 0)
		if !ok || got != v {
			t.Fatalf("varint %d 往返失败: got=%d ok=%v", v, got, ok)
		}
	}
}

// 发送帧结构 + 收包往返：buildIMAPIBody 造帧（外层 f1=cmd=100），collectChat 能抠回文本。
func TestBuildAndCollectRoundTrip(t *testing.T) {
	c := New("sid=abc", "3249781169", "3249781169")
	content := jsonNoEscape(imapiTextContent{AweType: 700, Type: 0, RichTextInfos: []any{}, Text: "你好&<world>"})
	payload, cmid := c.buildIMAPIBody("0:1:111:222", 0, content)
	if len(cmid) != 36 {
		t.Fatalf("clientMsgId 应为 uuid: %q", cmid)
	}
	top := decodeTop(payload)
	if v, ok := searchPath(top, []int{1}); !ok || v != 100 { // imapi 外层 f1=cmd=100
		t.Fatalf("外层 field1(cmd) 期望 100，得 %d ok=%v", v, ok)
	}
	var items []chatItem
	c.collectChat(top, &items, "", "", "", "")
	found := false
	for _, it := range items {
		if it.text == "你好&<world>" && it.aweType == 700 {
			found = true
		}
	}
	if !found {
		t.Fatalf("未能从自造帧解回文本，items=%+v", items)
	}
}

// JSON 内容编码：不转义 & < > /，中文原样（对应 UNESCAPED_UNICODE|UNESCAPED_SLASHES）。
func TestJSONNoEscape(t *testing.T) {
	s := jsonNoEscape(imapiTextContent{AweType: 700, Type: 0, RichTextInfos: []any{}, Text: "a&b<c>d/e 中文"})
	if strings.Contains(s, `\u`) {
		t.Fatalf("不该出现 \\u 转义: %s", s)
	}
	if strings.Contains(s, `\/`) {
		t.Fatalf("不该转义斜杠: %s", s)
	}
	if !strings.Contains(s, `"text":"a&b<c>d/e 中文"`) {
		t.Fatalf("text 字段应原样保留 < > & / 中文: %s", s)
	}
}

// 图片消息解析：aweType 2702 → 抠出 skey 与 origin_url_list。
func TestParseImageItem(t *testing.T) {
	c := New("", "999", "999")
	raw := `{"text":"[图片]","aweType":2702,"resource_url":{"skey":"deadbeef","origin_url_list":["https://a/x"],"md5":"m1"}}`
	var obj map[string]any
	mustJSON(t, raw, &obj)
	p, ok := c.parseChatJsonItem(obj, "888", "999")
	if !ok || p.image == nil {
		t.Fatalf("应解出图片: ok=%v img=%v", ok, p.image)
	}
	if p.image.Skey != "deadbeef" || p.image.PickURL() != "https://a/x" || p.image.Md5 != "m1" {
		t.Fatalf("图片字段不对: %+v", p.image)
	}
	if p.direction != "recv" { // sender 888 != self 999
		t.Fatalf("方向应为 recv，得 %s", p.direction)
	}
}

// 真实的纯图片消息（无 text 字段）也要能解出，text 占位 [图片]。
func TestParseImageNoText(t *testing.T) {
	c := New("", "999", "999")
	raw := `{"aweType":2702,"check_pics":[],"cover_height":600,"cover_width":600,"from_gallery":1,"md5":"c78e26432cdf5915c2661aaaa9ce4e03","ref_msg_info":{"comment":""},"resource_url":{"data_size":73119,"large_url_list":["https://p26-sign.douyinpic.com/x~tplv-x-get:large.image?a=1"],"md5":"c78e26432cdf5915c2661aaaa9ce4e03","medium_url_list":["https://m/medium"],"oid":"tos-cn-o-00061/bf454062d9b449f3a57783cd4f1db9bf","origin_url_list":["https://p3-sign.douyinpic.com/x~tplv-x-get:.image?a=1"],"skey":"fde318b1d9cee269ceb32ed6545cc0e81c0674649e297438c6737e2a9445c1ef","thumb_url_list":["https://t/thumb"]}}`
	var obj map[string]any
	mustJSON(t, raw, &obj)
	p, ok := c.parseChatJsonItem(obj, "888", "999")
	if !ok || p.image == nil {
		t.Fatalf("纯图片消息应解出: ok=%v img=%v", ok, p.image)
	}
	if p.text != "[图片]" {
		t.Fatalf("无文本应占位 [图片]，得 %q", p.text)
	}
	if p.image.Skey != "fde318b1d9cee269ceb32ed6545cc0e81c0674649e297438c6737e2a9445c1ef" {
		t.Fatalf("skey 不对: %s", p.image.Skey)
	}
	if !strings.Contains(p.image.PickURL(), "~tplv-x-get:.image") { // 优先 origin
		t.Fatalf("PickURL 应取 origin: %s", p.image.PickURL())
	}
	if p.image.Md5 != "c78e26432cdf5915c2661aaaa9ce4e03" || p.direction != "recv" {
		t.Fatalf("md5/方向不对: %+v %s", p.image, p.direction)
	}
}

// 自己发的消息应判为 sent。
func TestSelfDirection(t *testing.T) {
	c := New("", "999", "999")
	var obj map[string]any
	mustJSON(t, `{"text":"hi","aweType":700}`, &obj)
	p, _ := c.parseChatJsonItem(obj, "999", "999")
	if p.direction != "sent" {
		t.Fatalf("应为 sent，得 %s", p.direction)
	}
}

// access_key = md5(fpid+appkey+deviceId+salt)。
func TestAccessKey(t *testing.T) {
	c := New("", "3249781169", "3249781169")
	got := c.computeAccessKey("3249781169")
	// 复核值：md5("9" + "e1bd35ec9db7b8d846de66ed140b1ad9" + "3249781169" + "f8a69f1719916z")
	if len(got) != 32 {
		t.Fatalf("access_key 长度应为 32：%s", got)
	}
	if got != md5Ref("9e1bd35ec9db7b8d846de66ed140b1ad93249781169f8a69f1719916z") {
		t.Fatalf("access_key 不匹配参考实现：%s", got)
	}
}

// rawURLEncode = PHP rawurlencode（RFC3986）。
func TestRawURLEncode(t *testing.T) {
	cases := map[string]string{
		"[]":     "%5B%5D",
		"a b":    "a%20b",
		"a-_.~z": "a-_.~z",
		"中":      "%E4%B8%AD",
		"a=b&c":  "a%3Db%26c",
	}
	for in, want := range cases {
		if got := rawURLEncode(in); got != want {
			t.Fatalf("rawURLEncode(%q)=%q want %q", in, got, want)
		}
	}
}
