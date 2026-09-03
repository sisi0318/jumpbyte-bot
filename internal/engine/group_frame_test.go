package engine

import (
	"os"
	"testing"
)

// 用真机群聊表情帧跑收包路径：验证群消息(纯数字 conv_id)、表情(aweType 507)不被丢、
// sender/sec_uid 都解出。回归 group 收包三处修复：表情解析、is_group、sec_uid 解码。
func TestGroupEmojiFrame(t *testing.T) {
	raw, err := os.ReadFile("testdata/group_emoji_frame.bin")
	if err != nil {
		t.Skip("无 group_emoji_frame.bin")
	}
	self := "3916922778814715"
	c := New("", self, self)
	items := c.extractChatItems(raw, self)
	if len(items) != 1 {
		t.Fatalf("应解出 1 条，得 %d", len(items))
	}
	it := items[0]
	if it.convID != "7681236801654178341" || !isGroupConv(it.convID) {
		t.Fatalf("群 conv_id 不对: %q", it.convID)
	}
	if it.aweType != 507 || it.emoji == nil {
		t.Fatalf("表情应被解析(aweType=507 + emoji)，得 awe=%d emoji=%v", it.aweType, it.emoji)
	}
	if it.emoji.DisplayName == "" || it.emoji.URL == "" {
		t.Fatalf("表情缺 display_name/url: %+v", it.emoji)
	}
	if it.text != it.emoji.DisplayName {
		t.Fatalf("表情占位文本应为表情名，得 %q", it.text)
	}
	if it.senderID != self {
		t.Fatalf("sender 不对: %q", it.senderID)
	}
	if it.senderMs4 == "" || it.senderMs4[:3] != "MS4" {
		t.Fatalf("sec_uid 未解出(解码器应把 MS4 串判为字符串): %q", it.senderMs4)
	}
}
