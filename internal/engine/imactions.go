package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// 撤回 / 表情 / 回复：共用 imapi 外壳(buildEnvelope)，只有 cmd 号与内层不同。逆自真机 HAR。

// Recall 撤回一条消息（cmd 702，f702{conv_id, short_id, 1, server_msg_id}）。
func (c *Client) Recall(convID string, shortID, serverMsgID uint64) error {
	if strings.TrimSpace(c.CkUid) == "" {
		return fmt.Errorf("未初始化：缺少 user_id")
	}
	shortID = c.resolveShort(convID, shortID)
	f702 := concat(
		encodeLenDelimS(1, convID),
		encodeFieldVarint(2, shortID),
		encodeFieldVarint(3, 1),
		encodeFieldVarint(4, serverMsgID),
	)
	body := c.buildEnvelope(702, 702, f702, "0")
	rb, err := c.postIMAPIRaw(imapiRecallURL, body)
	if err != nil {
		return err
	}
	if status, ok := searchPath(decodeTop(rb), []int{3}); ok && status != 0 {
		return fmt.Errorf("撤回失败 status=%d: %s", status, snippet(rb))
	}
	return nil
}

// -- 表情（cmd 100，aweType 507）------------------------------------------

type imapiEmojiURL struct {
	Height   int      `json:"height"`
	DataSize int      `json:"data_size"`
	URI      string   `json:"uri"`
	URLList  []string `json:"url_list"`
	Width    int      `json:"width"`
}

type imapiEmojiContent struct {
	DisplayName            string        `json:"display_name"`
	Height                 int           `json:"height"`
	Width                  int           `json:"width"`
	ImageID                int           `json:"image_id"`
	ImageType              string        `json:"image_type"`
	PackageID              int           `json:"package_id"`
	ShowNotice             bool          `json:"show_notice"`
	ResourceType           int           `json:"resource_type"`
	UpdateConversationTime bool          `json:"updateConversationTime"`
	URL                    imapiEmojiURL `json:"url"`
	CreatedAt              int           `json:"createdAt"`
	IsCard                 bool          `json:"is_card"`
	MsgHint                string        `json:"msgHint"`
	AweType                int           `json:"aweType"`
}

// EmojiSpec 发表情入参。URL 是表情图地址（可从表情库拿）。
type EmojiSpec struct {
	DisplayName   string
	URL           string
	Width, Height int
	ImageType     string // 默认 png
	PackageID     int
}

// SendEmojiResult 发一个表情。
func (c *Client) SendEmojiResult(convID string, shortID uint64, e EmojiSpec) (SendResult, error) {
	if e.Width == 0 {
		e.Width = 100
	}
	if e.Height == 0 {
		e.Height = 100
	}
	if e.ImageType == "" {
		e.ImageType = "png"
	}
	content := imapiEmojiContent{
		DisplayName: e.DisplayName, Height: e.Height, Width: e.Width,
		ImageID: 0, ImageType: e.ImageType, PackageID: e.PackageID,
		ShowNotice: false, ResourceType: 4, UpdateConversationTime: true,
		URL:       imapiEmojiURL{URI: e.URL, URLList: []string{e.URL}},
		CreatedAt: 0, IsCard: false, MsgHint: "", AweType: 507,
	}
	return c.dispatchSend(convID, shortID, jsonNoEscape(content), msgTypeEmoji)
}

// -- 回复（cmd 100，content 带 refmsg_*）-----------------------------------

type imapiReplyContent struct {
	RefmsgType    int    `json:"refmsg_type"`
	Content       string `json:"content"`
	RefmsgUID     string `json:"refmsg_uid"`
	RefmsgSecUID  string `json:"refmsg_sec_uid"`
	Nickname      string `json:"nickname"`
	RefmsgContent string `json:"refmsg_content"`
	Version       int    `json:"version"`
	ItemID        string `json:"itemId"`
	SceneType     int    `json:"scene_type"`
}

// ReplySpec 回复入参。RefText 是被回复消息的原文（用于重建 refmsg_content）。
type ReplySpec struct {
	Text              string
	RefUID, RefSecUID string
	Nickname          string
	RefText           string
}

// SendReplyResult 回复一条消息（引用原文）。
func (c *Client) SendReplyResult(convID string, shortID uint64, r ReplySpec) (SendResult, error) {
	refContent := jsonNoEscape(imapiTextContent{AweType: 700, Type: 0, RichTextInfos: []any{}, Text: r.RefText})
	content := imapiReplyContent{
		RefmsgType: 7, Content: r.Text, RefmsgUID: r.RefUID, RefmsgSecUID: r.RefSecUID,
		Nickname: r.Nickname, RefmsgContent: refContent, Version: 1, ItemID: "", SceneType: 1,
	}
	return c.dispatchSend(convID, shortID, jsonNoEscape(content), msgTypeText)
}

// ParseUint 宽松解析无符号整数（供网关拼 server_msg_id / short_id）。
func ParseUint(s string) uint64 {
	n, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return n
}
