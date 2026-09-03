package engine

import (
	"strings"
	"testing"
)

// WS 群聊发送帧结构：解回自己造的帧，逐层核对 外层/内层(安卓)/field100(复用 conv_type/msg_type/content)。
func TestBuildWSSendPayloadGroup(t *testing.T) {
	c := New("ck", "3916922778814715", "3916922778814715")
	convID := "7681236801654178341" // 群会话（纯数字）
	content := jsonNoEscape(imapiTextContent{AweType: 700, Type: 0, RichTextInfos: []any{}, Text: "hi"})

	convType, short := c.resolveConvSend(convID, 0)
	if convType != convTypeGroup || short == 0 {
		t.Fatalf("群应 conv_type=2 且 short=群号, 得 type=%d short=%d", convType, short)
	}
	payload, cmid := c.buildWSSendPayload(convID, short, content, convType, msgTypeText)
	if len(cmid) != 36 {
		t.Fatalf("cmid 非 uuid: %q", cmid)
	}

	top := decodeTop(payload)
	if v, _ := searchPath(top, []int{3}); v != 5 {
		t.Fatal("外层 f3 应=5")
	}
	if v, _ := searchPath(top, []int{4}); v != 1 {
		t.Fatal("外层 f4 应=1")
	}
	inner := firstMsg(top, 8)
	if inner == nil {
		t.Fatal("无内层 f8")
	}
	if v, _ := searchPath(inner, []int{1}); v != 100 {
		t.Fatal("内层 f1 应=100(cmd)")
	}
	if s := firstStr(inner, 11); s != "android" {
		t.Fatalf("内层 f11 应=android，得 %q", s)
	}
	if firstStr(inner, 21) != "douyin" || firstStr(inner, 22) != "douyin_main" {
		t.Fatalf("内层 biz/access 不对: %q/%q", firstStr(inner, 21), firstStr(inner, 22))
	}

	f100 := firstMsg(firstMsg(inner, 8), 100) // inner.f8=msgWrapper → f100=field100
	if f100 == nil {
		t.Fatal("无 field100")
	}
	if firstStr(f100, 1) != convID {
		t.Fatalf("field100.f1 conv_id: %q", firstStr(f100, 1))
	}
	if firstVarint(f100, 2) != uint64(convTypeGroup) {
		t.Fatalf("field100.f2 conv_type 应=2，得 %d", firstVarint(f100, 2))
	}
	if firstVarint(f100, 3) != short {
		t.Fatalf("field100.f3 short 应=%d，得 %d", short, firstVarint(f100, 3))
	}
	if firstVarint(f100, 6) != uint64(msgTypeText) {
		t.Fatalf("field100.f6 msg_type 应=7，得 %d", firstVarint(f100, 6))
	}
	// WS 文本走 TS ch1(douyin_main) 形状：带 instruction_type / item_type_local，而非 web 的 richTextInfos
	s := firstStr(f100, 4)
	if !strings.Contains(s, `"text":"hi"`) || !strings.Contains(s, `"instruction_type":0`) ||
		!strings.Contains(s, `"item_type_local":-1`) {
		t.Fatalf("field100.f4 应为 ch1 文本形状: %q", s)
	}
	if strings.Contains(s, "richTextInfos") {
		t.Fatalf("ch1 文本不该含 richTextInfos(那是 web 形状): %q", s)
	}
	if firstStr(f100, 8) != cmid {
		t.Fatalf("field100.f8 cmid: %q", firstStr(f100, 8))
	}
}

// wsAdaptContent 只换纯文本；引用回复(refmsg_*)与图片/表情/视频原样放行。
func TestWSAdaptContent(t *testing.T) {
	// 纯文本 → ch1 形状
	web := jsonNoEscape(imapiTextContent{AweType: 700, Type: 0, RichTextInfos: []any{}, Text: "hi"})
	got := wsAdaptContent(web, msgTypeText)
	if !strings.Contains(got, `"instruction_type":0`) || strings.Contains(got, "richTextInfos") {
		t.Fatalf("纯文本应换成 ch1: %q", got)
	}
	// 回复(msgTypeText 但含 refmsg_type)不该被改
	reply := `{"refmsg_type":7,"content":"hi","text":""}`
	if wsAdaptContent(reply, msgTypeText) != reply {
		t.Fatalf("回复不该被改: %q", wsAdaptContent(reply, msgTypeText))
	}
	// 图片(非文本)原样
	img := `{"aweType":2702,"resource_url":{}}`
	if wsAdaptContent(img, msgTypeImage) != img {
		t.Fatalf("图片不该被改: %q", wsAdaptContent(img, msgTypeImage))
	}
}

// 图片走 WS 时 msg_type 应=27（复用内容层的类型判定）。
func TestBuildWSSendPayloadImageMsgType(t *testing.T) {
	c := New("ck", "999", "999")
	_, cmid := c.buildWSSendPayload("0:1:1:2", 5, `{"aweType":2702}`, convTypeSingle, msgTypeImage)
	if cmid == "" {
		t.Fatal("cmid 空")
	}
	payload, _ := c.buildWSSendPayload("0:1:1:2", 5, `{"aweType":2702}`, convTypeSingle, msgTypeImage)
	f100 := firstMsg(firstMsg(firstMsg(decodeTop(payload), 8), 8), 100)
	if firstVarint(f100, 6) != uint64(msgTypeImage) {
		t.Fatalf("图片 msg_type 应=27，得 %d", firstVarint(f100, 6))
	}
}

// 回执匹配：按 s:client_message_id 命中本条，取 f3=server_msg_id；风控 BLOCK 判未送达。
func TestMatchSendAck(t *testing.T) {
	cmid := "abc-123-cmid"
	build := func(extra ...[]byte) []ProtoField {
		msg := concat(append([][]byte{
			encodeFieldVarint(3, 999888777),
			encodeKvPair(9, "s:client_message_id", cmid),
		}, extra...)...)
		frame := encodeLenDelim(8, encodeLenDelim(6, encodeLenDelim(500, encodeLenDelim(5, msg))))
		return decodeTop(frame)
	}

	var ack wsAck
	if !matchSendAck(build(), cmid, &ack) {
		t.Fatal("未匹配到回执")
	}
	if ack.serverMsgID != "999888777" {
		t.Fatalf("server_msg_id: %q", ack.serverMsgID)
	}
	if ack.blocked {
		t.Fatal("不该判拦截")
	}

	// 别的 cmid 不该命中
	var other wsAck
	if matchSendAck(build(), "someone-else", &other) {
		t.Fatal("不同 cmid 不该命中")
	}

	// shark=BLOCK → 判未送达
	var blocked wsAck
	matchSendAck(build(encodeKvPair(9, "s:vcd_shark_decision", "BLOCK")), cmid, &blocked)
	if !blocked.blocked || blocked.reason != "shark=BLOCK" {
		t.Fatalf("应判拦截 shark=BLOCK: %+v", blocked)
	}

	// callback 风控码 → 判未送达
	var cb wsAck
	matchSendAck(build(encodeKvPair(9, "im_callback_status_code", "8101")), cmid, &cb)
	if !cb.blocked || cb.reason != "callback=8101" {
		t.Fatalf("应判拦截 callback=8101: %+v", cb)
	}
}
