package engine

import (
	"os"
	"testing"
)

// 用真机 conv/list 响应验证解析：应解出 4 条会话，首条是群、带成员(uid+sec_uid)。
func TestParseConvList(t *testing.T) {
	rb, err := os.ReadFile("testdata/imapi_convlist_resp.bin")
	if err != nil {
		t.Skip("无 conv/list HAR testdata")
	}
	convs := parseConvListResp(rb)
	if len(convs) != 4 {
		t.Fatalf("应解出 4 条会话，得 %d", len(convs))
	}
	g := convs[0]
	if !g.IsGroup || g.ConvType != 2 {
		t.Fatalf("首条应为群(conv_type=2)：%+v", g)
	}
	if g.ConvID != "7681236801654178341" || g.ShortID != "7681236801654178341" {
		t.Fatalf("群 conv_id/short 不对：id=%s short=%s", g.ConvID, g.ShortID)
	}
	if len(g.Members) < 2 {
		t.Fatalf("群成员应 >=2，得 %d", len(g.Members))
	}
	ok := false
	for _, m := range g.Members {
		if m.UID != "" && m.SecUID != "" {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("群成员缺 uid/sec_uid：%+v", g.Members)
	}
	if g.LastMsgTime == 0 {
		t.Fatal("群 last_msg_time 应非零")
	}
	t.Logf("群 id=%s 成员=%d owner=%s avatar=%.48s last=%d", g.ConvID, len(g.Members), g.OwnerUID, g.Avatar, g.LastMsgTime)
}
