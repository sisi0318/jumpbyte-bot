package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// 会话列表(cmd 2006)：私信 + 群聊都在里面。逆自真机 HAR imapi /v1/conversation/list。
const imapiConvListURL = "https://imapi.douyin.com/v1/conversation/list"

// ConvMember 会话成员。
type ConvMember struct {
	UID    string `json:"uid"`
	SecUID string `json:"sec_uid"`
	Role   int    `json:"role"`
}

// Conversation 一条会话（群或单聊）。
type Conversation struct {
	ConvID      string       `json:"conv_id"`
	IsGroup     bool         `json:"is_group"`
	ConvType    int          `json:"conv_type"`
	ShortID     string       `json:"conv_short_id"`
	Avatar      string       `json:"avatar"`
	OwnerUID    string       `json:"owner_uid"`
	LastMsgTime int64        `json:"last_msg_time"`
	Members     []ConvMember `json:"members"`
}

// ListConversations 拉会话列表（cmd 2006）。count 拉取条数（默认 20）。
func (c *Client) ListConversations(count int) ([]Conversation, error) {
	if strings.TrimSpace(c.CkUid) == "" {
		return nil, fmt.Errorf("未初始化：缺少 user_id")
	}
	if count <= 0 {
		count = 20
	}
	// 内层参数照 HAR：f1=1 f2=0(cursor) f3=2 f4=count
	inner := concat(
		encodeFieldVarint(1, 1),
		encodeFieldVarint(2, 0),
		encodeFieldVarint(3, 2),
		encodeFieldVarint(4, uint64(count)),
	)
	body := c.buildEnvelope(2006, 2006, inner, "0") // device_id 照 HAR 为 "0"
	rb, err := c.postIMAPIRaw(imapiConvListURL, body)
	if err != nil {
		return nil, err
	}
	if status, ok := searchPath(decodeTop(rb), []int{3}); ok && status != 0 {
		return nil, fmt.Errorf("拉会话列表失败 status=%d: %s", status, snippet(rb))
	}
	return parseConvListResp(rb), nil
}

// parseConvListResp 解会话列表响应：f6 → f2006 → 重复的 f1 每个是一条会话。
func parseConvListResp(rb []byte) []Conversation {
	var out []Conversation
	for _, f6 := range childMsgs(decodeTop(rb), 6) {
		for _, box := range childMsgs(f6, 2006) {
			for _, cv := range childMsgs(box, 1) {
				out = append(out, parseConversation(cv))
			}
		}
	}
	return out
}

func parseConversation(cv []ProtoField) Conversation {
	conv := Conversation{ConvID: firstStr(cv, 1), ConvType: int(firstVarint(cv, 3))}
	conv.IsGroup = conv.ConvType == convTypeGroup
	if sid := firstVarint(cv, 2); sid != 0 {
		conv.ShortID = strconv.FormatUint(sid, 10)
	}
	// 成员 f6 → 重复 f1{uid=f1, role=f3, sec_uid=f5}
	for _, part := range childMsgs(cv, 6) {
		for _, mem := range childMsgs(part, 1) {
			uid := firstVarint(mem, 1)
			if uid == 0 {
				continue
			}
			conv.Members = append(conv.Members, ConvMember{
				UID: strconv.FormatUint(uid, 10), Role: int(firstVarint(mem, 3)), SecUID: firstStr(mem, 5),
			})
		}
	}
	// 核心信息 f50：avatar=f7, owner=f12
	if core := firstMsg(cv, 50); core != nil {
		conv.Avatar = firstStr(core, 7)
		if o := firstVarint(core, 12); o != 0 {
			conv.OwnerUID = strconv.FormatUint(o, 10)
		}
	}
	// 状态 f51：last_msg_time=f10
	if st := firstMsg(cv, 51); st != nil {
		conv.LastMsgTime = int64(firstVarint(st, 10))
	}
	return conv
}

// -- ProtoField 小工具（repeated 感知；searchPath 只取首个 varint，这里补齐）------

func childMsgs(fields []ProtoField, num int) [][]ProtoField {
	var out [][]ProtoField
	for _, f := range fields {
		if f.Field == num && f.Type == "message" {
			out = append(out, f.VMsg)
		}
	}
	return out
}

func firstMsg(fields []ProtoField, num int) []ProtoField {
	for _, f := range fields {
		if f.Field == num && f.Type == "message" {
			return f.VMsg
		}
	}
	return nil
}

func firstStr(fields []ProtoField, num int) string {
	for _, f := range fields {
		if f.Field == num && f.Type == "string" {
			return f.VStr
		}
	}
	return ""
}

func firstVarint(fields []ProtoField, num int) uint64 {
	for _, f := range fields {
		if f.Field == num && f.Type == "varint" {
			return f.VUint
		}
	}
	return 0
}
