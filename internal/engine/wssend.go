package engine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// WS 发送通道：走安卓 frontier(/ws/v2) 发 cmd100 帧，绕开 HTTP imapi(douyin_pc web_sdk)群聊被风控(7523)的问题。
// 逆自 TS ImClient：buildSendMessage(ch1 douyin_main) + buildAndroidCmd100Inner + wrapAndroidCmd100Outer + drainSendAck。
// content / conv_type / msg_type 与 HTTP 通道完全复用；这里只做"安卓外壳 + 发帧 + 等回执"。
//
// 帧三层：
//
//	外层(frontier)  f1 seq f2 ts f3 5 f4 1 f5 KV{cmd100..} f6/f7 "pb" f8=inner
//	内层(cmd100)    f1 100 f2 seq f3 sdkver f7 build f8=msgWrapper f9 uid f11 "android" f15 KV.. f21/f22 biz/access
//	msgWrapper      f100 = field100{ f1 conv_id f2 conv_type f3 short f4 content f5 ext.. f6 msg_type [f7 ticket] f8 cmid f12 ext12.. }

const (
	wsSendBiz    = "douyin"
	wsSendAccess = "douyin_main"
)

// wsCh1TextContent 安卓 douyin_main(ch1) 的文本 content（字段顺序照 TS buildSendMessage case 1）。
// 与 HTTP(web_sdk)的 imapiTextContent 形状不同，走 WS 时用这套更贴近真机安卓端。
type wsCh1TextContent struct {
	Type            int    `json:"type"`
	InstructionType int    `json:"instruction_type"`
	ItemTypeLocal   int    `json:"item_type_local"`
	Text            string `json:"text"`
	CreatedAt       int    `json:"createdAt"`
	IsCard          bool   `json:"is_card"`
	MsgHint         string `json:"msgHint"`
	AweType         int    `json:"aweType"`
}

// wsAdaptContent 把上层(按 HTTP/web 形状)造好的 content 适配成 WS(安卓 ch1)形状。
// 目前仅文本换成 ch1 shape（群聊主要场景）；图片/表情/视频暂复用原 content。
func wsAdaptContent(contentJSON string, msgType int) string {
	if msgType != msgTypeText {
		return contentJSON
	}
	var m map[string]any
	if json.Unmarshal([]byte(contentJSON), &m) != nil {
		return contentJSON
	}
	// 只换"纯文本"(aweType 700 + text)；引用回复(refmsg_*)也走 msgTypeText，但结构不同，原样放行。
	if _, isReply := m["refmsg_type"]; isReply {
		return contentJSON
	}
	text, ok := m["text"].(string)
	if !ok || toInt(m["aweType"]) != 700 {
		return contentJSON
	}
	return jsonNoEscape(wsCh1TextContent{
		Type: 0, InstructionType: 0, ItemTypeLocal: -1, Text: text,
		CreatedAt: 0, IsCard: false, MsgHint: "", AweType: 700,
	})
}

// sendViaWS 通过安卓 frontier WS 发一条消息，读回执拿 server_msg_id；被风控拦截则明确报错。
func (c *Client) sendViaWS(convID string, shortID uint64, content string, msgType int) (SendResult, error) {
	convType, short := c.resolveConvSend(convID, shortID)
	res := SendResult{ConvID: convID, SelfUID: c.CkUid, ConvShortID: u64str(short)}
	if c.CkUid == "" {
		return res, fmt.Errorf("未初始化：缺少 user_id")
	}
	payload, cmid := c.buildWSSendPayload(convID, short, content, convType, msgType)
	res.ClientMsgID = cmid

	conn, err := c.makeClient(c.buildAndroidSendWsURL())
	if err != nil {
		return res, fmt.Errorf("WS 连接失败: %w", err)
	}
	defer conn.Close()
	if err := conn.Send(payload); err != nil {
		return res, fmt.Errorf("WS 发送失败: %w", err)
	}

	ack := c.drainSendAck(conn, cmid, 4*time.Second)
	if ack.serverMsgID != "" {
		res.ServerMsgID = ack.serverMsgID
	}
	if ack.blocked {
		return res, fmt.Errorf("WS 发送被拦截(未送达): %s", ack.reason)
	}
	if ack.serverMsgID == "" {
		return res, fmt.Errorf("WS 发送超时：4s 内未收到回执（可能被静默拦截或会话号不对）")
	}
	return res, nil
}

// buildWSSendPayload 组一条 WS cmd100 发送帧，返回 (帧字节, client_msg_id)。
func (c *Client) buildWSSendPayload(convID string, shortID uint64, content string, convType, msgType int) ([]byte, string) {
	seqID := c.nextSeq()
	ms := time.Now().UnixMilli()
	cmid := uuid.NewString()

	field100 := concat(
		encodeLenDelimS(1, convID),
		encodeFieldVarint(2, uint64(convType)),
		encodeFieldVarint(3, shortID),
		encodeLenDelimS(4, wsAdaptContent(content, msgType)),
	)
	for _, kv := range androidSendExt(ms) {
		field100 = append(field100, encodeKvPair(5, kv[0], kv[1])...)
	}
	field100 = append(field100, encodeFieldVarint(6, uint64(msgType))...)
	// f7 ticket 服务端下发、按会话缓存；首发为空，多数会话不需要，暂不携带。
	field100 = append(field100, encodeLenDelimS(8, cmid)...)
	for _, kv := range androidSendExt12 {
		field100 = append(field100, encodeKvPair(12, kv[0], kv[1])...)
	}

	msgWrapper := encodeLenDelim(100, field100)
	return wrapAndroidCmd100Outer(c.buildAndroidCmd100Inner(msgWrapper, seqID), seqID, ms), cmid
}

// buildAndroidCmd100Inner 安卓 IM SDK 的 cmd100 内层外壳（对应 HTTP 侧 buildEnvelope 的安卓版）。
func (c *Client) buildAndroidCmd100Inner(msgWrapper []byte, seqID int) []byte {
	dev := c.CkUid
	parts := concat(
		encodeFieldVarint(1, 100),
		encodeFieldVarint(2, uint64(seqID)),
		encodeLenDelimS(3, awemeSDKVersion),
		encodeFieldVarint(5, 1),
		encodeFieldVarint(6, 0),
		encodeLenDelimS(7, awemeBuildNumber2),
		encodeLenDelim(8, msgWrapper),
		encodeLenDelimS(9, dev),
		encodeLenDelimS(10, awemeChannel),
		encodeLenDelimS(11, "android"),
		encodeLenDelimS(12, awemeDeviceType),
		encodeLenDelimS(13, awemeOSVersion),
		encodeLenDelimS(14, awemeVersionCode),
	)
	for _, kv := range [][2]string{
		{"app_name", awemeAppName}, {"iid", dev}, {"version_code", awemeVersionCode},
		{"net_mcc_mnc", "46000"}, {"aid", awemeAID}, {"flow-tag", "new"}, {"user-agent", awemeUA},
	} {
		parts = append(parts, encodeKvPair(15, kv[0], kv[1])...)
	}
	parts = append(parts, encodeFieldVarint(18, 0)...)
	parts = append(parts, encodeLenDelimS(21, wsSendBiz)...)
	parts = append(parts, encodeLenDelimS(22, wsSendAccess)...)
	return parts
}

// wrapAndroidCmd100Outer frontier 上行外层帧。
func wrapAndroidCmd100Outer(inner []byte, seqID int, ms int64) []byte {
	out := concat(
		encodeFieldVarint(1, uint64(seqID)),
		encodeFieldVarint(2, uint64(ms)),
		encodeFieldVarint(3, 5),
		encodeFieldVarint(4, 1),
	)
	seq := strconv.Itoa(seqID)
	for _, kv := range [][2]string{
		{"msg_type", "cmd100"}, {"seq_id", seq}, {"cmd", "100"}, {"is-retry", "0"}, {"flow-tag", "new"},
	} {
		out = append(out, encodeKvPair(5, kv[0], kv[1])...)
	}
	out = append(out, encodeLenDelimS(6, "pb")...)
	out = append(out, encodeLenDelimS(7, "pb")...)
	out = append(out, encodeLenDelim(8, inner)...)
	return out
}

// androidSendExt ch1(douyin_main) 的 f5 ext KV（含时间戳，逆自 TS buildSendMessage case 1）。
func androidSendExt(ms int64) [][2]string {
	return [][2]string{
		{"s:ticket_mode", "0"},
		{"im_client_send_msg_time", strconv.FormatInt(ms-500, 10)},
		{"a:plv", "1"},
		{"a:access", wsSendAccess},
		{"s:biz_aid", awemeAID},
		{"chat_scene", "normal"},
		{"a:msg_scene", "1"},
		{"im_sdk_client_send_msg_time", strconv.FormatInt(ms-375, 10)},
		{"a:relation_type", "0:0"},
		{"a:smp_token_fetch", "11"},
		{"a:ntp_ready", "2"},
		{"s:sync_2_newdx", "1"},
		{"old_client_message_id", strconv.FormatInt(ms, 10)},
		{"s:mode", "0"},
		{"a:enter_method", "click_message"},
		{"a:biz", wsSendBiz},
		{"s:is_stranger", "false"},
		{"source_aid", awemeAID},
		{"s:saas_sdk", "false"},
		{"a:sync2dx", "1"},
		{"s:refer", "3"},
	}
}

var androidSendExt12 = [][2]string{
	{"s:reverse_creator_im_ex", "0"},
	{"a:from_role_ids", ""},
	{"s:im_creator_chat_opt_exp", "0"},
	{"s:send_ignore_ticket", "true"},
	{"s:im_chat_priv_opt_exp", "1"},
	{"a:to_role_ids", "[]"},
}

// -- 回执 -------------------------------------------------------------------

type wsAck struct {
	serverMsgID string
	blocked     bool
	reason      string
}

// drainSendAck 发完后读回执帧直到拿到本条的 ack 或超时。回执结构同收包帧：
// .8.6.500.5[*] 里 f9 KV[s:client_message_id]==本条 → f3=server_msg_id；风控看 shark/callback。
func (c *Client) drainSendAck(conn *WsConn, wantCmid string, timeout time.Duration) wsAck {
	deadline := time.Now().Add(timeout)
	var ack wsAck
	for time.Now().Before(deadline) {
		raw, err := conn.Receive(350 * time.Millisecond)
		if err != nil {
			break
		}
		if len(raw) == 0 {
			continue
		}
		if matchSendAck(decodeTop(raw), wantCmid, &ack) {
			break
		}
	}
	return ack
}

func matchSendAck(top []ProtoField, wantCmid string, ack *wsAck) bool {
	for _, f8 := range childMsgs(top, 8) {
		for _, f6 := range childMsgs(f8, 6) {
			for _, f500 := range childMsgs(f6, 500) {
				for _, msg := range childMsgs(f500, 5) {
					kv := collectKV(msg, 9)
					if kv["s:client_message_id"] != wantCmid {
						continue
					}
					if sid := firstVarint(msg, 3); sid != 0 {
						ack.serverMsgID = strconv.FormatUint(sid, 10)
					}
					if kv["s:vcd_shark_decision"] == "BLOCK" || blockedCallback(kv["im_callback_status_code"]) {
						ack.blocked = true
						if kv["s:vcd_shark_decision"] == "BLOCK" {
							ack.reason = "shark=BLOCK"
						} else {
							ack.reason = "callback=" + kv["im_callback_status_code"]
						}
					}
					return true
				}
			}
		}
	}
	return false
}

func blockedCallback(code string) bool {
	switch code {
	case "8101", "8610", "10502":
		return true
	}
	return false
}

// collectKV 收集重复 KV 字段(fieldNum){f1:key, f2:val}。
func collectKV(fields []ProtoField, fieldNum int) map[string]string {
	kv := map[string]string{}
	for _, f := range fields {
		if f.Field != fieldNum || f.Type != "message" {
			continue
		}
		var k, v string
		for _, sub := range f.VMsg {
			if sub.Field == 1 && sub.Type == "string" {
				k = sub.VStr
			}
			if sub.Field == 2 && sub.Type == "string" {
				v = sub.VStr
			}
		}
		if k != "" {
			kv[k] = v
		}
	}
	return kv
}
