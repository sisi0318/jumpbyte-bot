package engine

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// 我们硬拼的 body 应与真机 HAR 逐字节同构：静态字段的编码字节两边都在。
func TestBuildIMAPIBodyMatchesHAR(t *testing.T) {
	ref, err := os.ReadFile("testdata/imapi_send_text_req.bin")
	if err != nil {
		t.Skip("无 HAR testdata")
	}
	c := New("ck", "3916922778814715", "3249781169")
	convID := "0:1:2119508107991872:3916922778814715"
	var shortID uint64 = 7678270758086263345
	content := jsonNoEscape(imapiTextContent{AweType: 700, Type: 0, RichTextInfos: []any{}, Text: "qwq"})
	body, cmid := c.buildIMAPIBody(convID, shortID, content)
	if len(cmid) != 36 {
		t.Fatalf("clientMsgId 非 uuid: %q", cmid)
	}

	// 这些字段的精确编码字节，HAR 与我们各自都应包含
	static := map[string][]byte{
		"f1=cmd100":       encodeFieldVarint(1, 100),
		"f3=sdk_version":  encodeLenDelimS(3, webSDKVersion),
		"f7=build":        encodeLenDelimS(7, webBuildNumber),
		"f11=app":         encodeLenDelimS(11, webAppName),
		"f14=360000":      encodeLenDelimS(14, "360000"),
		"f18=1":           encodeFieldVarint(18, 1),
		"f21=biz":         encodeLenDelimS(21, webBiz),
		"f22=access":      encodeLenDelimS(22, webAccess),
		"kv session_aid":  encodeKvPair(15, "session_aid", webSessionAID),
		"kv app_name":     encodeKvPair(15, "app_name", webAppName),
		"kv browser_name": encodeKvPair(15, "browser_name", "Mozilla"),
		"f100.mentioned":  encodeKvPair(5, "s:mentioned_users", ""),
	}
	for name, want := range static {
		if !bytes.Contains(ref, want) {
			t.Fatalf("HAR 缺字段 %s（%x）——说明常量与真机不符", name, want)
		}
		if !bytes.Contains(body, want) {
			t.Fatalf("我们 body 缺字段 %s", name)
		}
	}
	// 动态但本例给定：conv_id / short_id / content 的编码字节应出现在我们 body 里
	if !bytes.Contains(body, encodeLenDelimS(1, convID)) {
		t.Fatal("body 缺 conv_id")
	}
	if !bytes.Contains(body, encodeFieldVarint(3, shortID)) {
		t.Fatal("body 缺 conv_short_id")
	}
	if !bytes.Contains(body, []byte(`"text":"qwq"`)) {
		t.Fatal("body 缺 content 文本")
	}
	// 这三个字段真机 HAR 里也应有（conv/short 同一条会话）
	if !bytes.Contains(ref, encodeFieldVarint(3, shortID)) {
		t.Fatal("HAR 缺该 conv_short_id（对照值取错？）")
	}
}

// 回执解析：status=0(OK) + server_msg_id 从 f6→f100→f1 取。
func TestParseSendResponse(t *testing.T) {
	rb, err := os.ReadFile("testdata/imapi_send_resp.bin")
	if err != nil {
		t.Skip("无 HAR testdata")
	}
	top := decodeTop(rb)
	if status, _ := searchPath(top, []int{3}); status != 0 {
		t.Fatalf("status 应为 0(OK)")
	}
	smid, ok := searchPath(top, []int{6, 100, 1})
	if !ok || smid != 7678690025631237690 {
		t.Fatalf("server_msg_id 应为 7678690025631237690, 得 %d ok=%v", smid, ok)
	}
}

// 图片 content 结构：resource_url 在前、aweType=2702 在后，字段顺序照 HAR。
func TestImageContentShape(t *testing.T) {
	var img imapiImageContent
	img.ResourceURL.Oid = "tos-cn-o-00061/abc"
	img.ResourceURL.Skey = "sk"
	img.ResourceURL.DataSize = 340499
	img.ResourceURL.Md5 = "m"
	img.CoverHeight = 1208
	img.CoverWidth = 1208
	img.CheckPics = []any{}
	img.Md5 = "m"
	img.FromGallery = 1
	img.AweType = 2702
	s := jsonNoEscape(img)
	if !strings.HasPrefix(s, `{"resource_url":{"oid":"tos-cn-o-00061/abc","skey":"sk","data_size":340499,"md5":"m"}`) {
		t.Fatalf("resource_url 段不对: %s", s)
	}
	if !strings.HasSuffix(s, `"from_gallery":1,"aweType":2702}`) {
		t.Fatalf("尾部字段顺序不对: %s", s)
	}
}
