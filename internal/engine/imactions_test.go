package engine

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// 撤回 body：cmd=702，f702{conv_id, short_id, 1, server_msg_id} 的编码字节应出现在 HAR 里。
func TestRecallBodyMatchesHAR(t *testing.T) {
	ref, err := os.ReadFile("testdata/imapi_recall_req.bin")
	if err != nil {
		t.Skip("无 HAR")
	}
	// HAR 值（来自 [201] 解码）
	convID := "0:1:2119508107991872:3916922778814715"
	var short uint64 = 7678270758086263345
	var smid uint64 = 7678710408102774321
	f702 := concat(
		encodeLenDelimS(1, convID),
		encodeFieldVarint(2, short),
		encodeFieldVarint(3, 1),
		encodeFieldVarint(4, smid),
	)
	if !bytes.Contains(ref, f702) {
		t.Fatal("HAR recall 里没有我们拼的 f702 字节（结构不符）")
	}
	// cmd=702（外壳 f1）
	if !bytes.Contains(ref, encodeFieldVarint(1, 702)) {
		t.Fatal("HAR recall f1 应为 702")
	}
	// 外壳 f21/f22 与发送一致
	if !bytes.Contains(ref, encodeLenDelimS(21, webBiz)) || !bytes.Contains(ref, encodeLenDelimS(22, webAccess)) {
		t.Fatal("recall 外壳 biz/access 不一致")
	}
}

// 表情 content：aweType 507 + 关键字段。
func TestEmojiContent(t *testing.T) {
	ref, err := os.ReadFile("testdata/imapi_emoji_req.bin")
	if err != nil {
		t.Skip("无 HAR")
	}
	c := New("ck", "999", "0")
	res, err := c.buildEmojiContentForTest("[微笑]", "https://x/y.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"resource_type":4`, `"aweType":507`, `"image_type":"png"`, `"display_name":"[微笑]"`, `"is_card":false`} {
		if !strings.Contains(res, k) {
			t.Fatalf("表情 content 缺 %s: %s", k, res)
		}
	}
	// HAR 里也应有 aweType 507 与 resource_type 4
	if !bytes.Contains(ref, []byte(`"aweType":507`)) || !bytes.Contains(ref, []byte(`"resource_type":4`)) {
		t.Fatal("HAR emoji 特征不符")
	}
}

// 回复 content：refmsg_type 7 + refmsg_content 内嵌原文 JSON。
func TestReplyContent(t *testing.T) {
	ref, err := os.ReadFile("testdata/imapi_reply_req.bin")
	if err != nil {
		t.Skip("无 HAR")
	}
	content := jsonNoEscape(imapiReplyContent{
		RefmsgType: 7, Content: "hi", RefmsgUID: "123", RefmsgSecUID: "MS4x",
		Nickname: "n", RefmsgContent: jsonNoEscape(imapiTextContent{AweType: 700, RichTextInfos: []any{}, Text: "orig"}),
		Version: 1, ItemID: "", SceneType: 1,
	})
	// 字段顺序照 HAR
	if !strings.HasPrefix(content, `{"refmsg_type":7,"content":"hi","refmsg_uid":"123","refmsg_sec_uid":"MS4x","nickname":"n","refmsg_content":"`) {
		t.Fatalf("回复 content 头部不对: %s", content)
	}
	if !strings.HasSuffix(content, `"version":1,"itemId":"","scene_type":1}`) {
		t.Fatalf("回复 content 尾部不对: %s", content)
	}
	// 内嵌原文应被正确转义
	if !strings.Contains(content, `"refmsg_content":"{\"aweType\":700,\"type\":0,\"richTextInfos\":[],\"text\":\"orig\"}"`) {
		t.Fatalf("refmsg_content 内嵌 JSON 不对: %s", content)
	}
	if !bytes.Contains(ref, []byte(`"refmsg_type":7`)) {
		t.Fatal("HAR reply 特征不符")
	}
}

// buildEmojiContentForTest 仅测试用：产出表情 content JSON。
func (c *Client) buildEmojiContentForTest(name, url string) (string, error) {
	content := imapiEmojiContent{
		DisplayName: name, Height: 100, Width: 100, ImageID: 0, ImageType: "png",
		PackageID: 0, ShowNotice: false, ResourceType: 4, UpdateConversationTime: true,
		URL: imapiEmojiURL{URI: url, URLList: []string{url}}, CreatedAt: 0, IsCard: false, MsgHint: "", AweType: 507,
	}
	return jsonNoEscape(content), nil
}
