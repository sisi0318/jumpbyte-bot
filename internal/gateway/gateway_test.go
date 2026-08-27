package gateway

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"gobot/internal/config"
	"gobot/internal/engine"
)

type fakeSender struct {
	lastConv, lastText string
	lastShort          uint64
	lastImg            engine.ImageAsset
}

func (f *fakeSender) SendTextEx(conv string, short uint64, text string) (engine.SendResult, error) {
	f.lastConv, f.lastShort, f.lastText = conv, short, text
	return engine.SendResult{ClientMsgID: "cmid1", ConvID: conv, SelfUID: "1000"}, nil
}

func (f *fakeSender) SendImageResult(conv string, short uint64, img engine.ImageAsset) (engine.SendResult, error) {
	f.lastConv, f.lastShort, f.lastImg = conv, short, img
	return engine.SendResult{ClientMsgID: "img1", ConvID: conv, SelfUID: "1000"}, nil
}

func (f *fakeSender) UploadImage(b []byte) (engine.ImageAsset, error) {
	return engine.ImageAsset{Oid: "tos/x", Skey: "sk", DataSize: len(b)}, nil
}

func (f *fakeSender) SendEmojiResult(conv string, short uint64, e engine.EmojiSpec) (engine.SendResult, error) {
	f.lastConv, f.lastShort = conv, short
	return engine.SendResult{ClientMsgID: "emo1", ConvID: conv}, nil
}

func (f *fakeSender) SendReplyResult(conv string, short uint64, r engine.ReplySpec) (engine.SendResult, error) {
	f.lastConv, f.lastShort, f.lastText = conv, short, r.Text
	return engine.SendResult{ClientMsgID: "rep1", ConvID: conv}, nil
}

func (f *fakeSender) SendVideoResult(conv string, short uint64, video, cover []byte, w, h int) (engine.SendResult, error) {
	f.lastConv, f.lastShort = conv, short
	return engine.SendResult{ClientMsgID: "vid1", ConvID: conv}, nil
}

func (f *fakeSender) Recall(conv string, short, smid uint64) error {
	f.lastConv, f.lastShort = conv, short
	return nil
}

func newTestGW() (*Gateway, *fakeSender) {
	cfg := &config.BotConfig{Host: "127.0.0.1", Port: 0, Token: "tok123"}
	acc := &config.Account{ID: "main", Name: "主号", UID: "1000", Enabled: true}
	fs := &fakeSender{}
	return New(cfg, acc, fs), fs
}

func post(t *testing.T, base, path, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", base+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestHealthNoAuth(t *testing.T) {
	g, _ := newTestGW()
	ts := httptest.NewServer(g.handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	if resp.StatusCode != 200 || m["code"].(float64) != 0 {
		t.Fatalf("health 应 200/code0: %d %v", resp.StatusCode, m)
	}
	data := m["data"].(map[string]any)
	if data["protocol"].(float64) != 1 {
		t.Fatalf("protocol 应为 1: %v", data)
	}
}

func TestAuth(t *testing.T) {
	g, _ := newTestGW()
	ts := httptest.NewServer(g.handler())
	defer ts.Close()
	if code, m := post(t, ts.URL, "/api/get_accounts", "", "{}"); code != 401 || m["code"].(float64) != 401 {
		t.Fatalf("无令牌应 401: %d %v", code, m)
	}
	if code, m := post(t, ts.URL, "/api/get_accounts", "wrong", "{}"); code != 401 || m["code"].(float64) != 401 {
		t.Fatalf("错令牌应 401: %d %v", code, m)
	}
	if code, m := post(t, ts.URL, "/api/get_accounts", "tok123", "{}"); code != 200 || m["code"].(float64) != 0 {
		t.Fatalf("对令牌应 200/code0: %d %v", code, m)
	}
}

func TestUnknownAndMethod(t *testing.T) {
	g, _ := newTestGW()
	ts := httptest.NewServer(g.handler())
	defer ts.Close()
	if code, m := post(t, ts.URL, "/api/nope", "tok123", "{}"); code != 404 || m["code"].(float64) != 404 {
		t.Fatalf("未知动作应 404: %d %v", code, m)
	}
	resp, _ := http.Get(ts.URL + "/api/get_accounts") // GET 非 POST
	if resp.StatusCode != 405 {
		t.Fatalf("非 POST 应 405: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSendTextValidationAndSuccess(t *testing.T) {
	g, fs := newTestGW()
	ts := httptest.NewServer(g.handler())
	defer ts.Close()

	if _, m := post(t, ts.URL, "/api/send_text", "tok123", `{"conv_id":"0:1:1:2"}`); m["code"].(float64) != 400 {
		t.Fatalf("缺 text 应 400: %v", m)
	}
	if _, m := post(t, ts.URL, "/api/send_text", "tok123", `{"text":"hi"}`); m["code"].(float64) != 400 {
		t.Fatalf("缺目标应 400: %v", m)
	}
	// to_uid 拼会话 + 成功
	_, m := post(t, ts.URL, "/api/send_text", "tok123", `{"to_uid":"2000","text":"hi"}`)
	if m["code"].(float64) != 0 {
		t.Fatalf("send_text 应成功: %v", m)
	}
	data := m["data"].(map[string]any)
	if data["conv_id"] != "0:1:1000:2000" || data["client_msg_id"] != "cmid1" {
		t.Fatalf("返回数据不对: %v", data)
	}
	if fs.lastConv != "0:1:1000:2000" || fs.lastText != "hi" {
		t.Fatalf("引擎收到参数不对: conv=%s text=%s", fs.lastConv, fs.lastText)
	}
}

func TestSendImage(t *testing.T) {
	g, fs := newTestGW()
	ts := httptest.NewServer(g.handler())
	defer ts.Close()
	if _, m := post(t, ts.URL, "/api/send_image", "tok123", `{"conv_id":"0:1:1:2"}`); m["code"].(float64) != 400 {
		t.Fatalf("缺 image 应 400: %v", m)
	}
	_, m := post(t, ts.URL, "/api/send_image", "tok123",
		`{"conv_id":"0:1:1:2","conv_short_id":"123","image":{"oid":"o1","skey":"s1","md5":"m","data_size":100,"cover_width":8,"cover_height":9}}`)
	if m["code"].(float64) != 0 {
		t.Fatalf("send_image 应成功: %v", m)
	}
	if fs.lastImg.Oid != "o1" || fs.lastImg.Skey != "s1" || fs.lastShort != 123 || fs.lastImg.CoverWidth != 8 {
		t.Fatalf("图片参数不对: %+v short=%d", fs.lastImg, fs.lastShort)
	}
}

func TestWebSocketHelloPongAndEvent(t *testing.T) {
	g, _ := newTestGW()
	ts := httptest.NewServer(g.handler())
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?access_token=tok123"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// 1) hello
	var hello map[string]any
	if err := c.ReadJSON(&hello); err != nil || hello["type"] != "hello" {
		t.Fatalf("首帧应为 hello: %v %v", hello, err)
	}
	// 2) ping → pong
	_ = c.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`))
	var pong map[string]any
	if err := c.ReadJSON(&pong); err != nil || pong["type"] != "pong" {
		t.Fatalf("应回 pong: %v %v", pong, err)
	}
	// 3) 服务端下推 message
	g.EmitMessage(engine.IncomingMessage{ConvID: "0:1:1:2", SenderID: "2", SenderMs4: "MS4x", Text: "yo"})
	var ev map[string]any
	if err := c.ReadJSON(&ev); err != nil {
		t.Fatal(err)
	}
	if ev["type"] != "message" || ev["conv_id"] != "0:1:1:2" || ev["text"] != "yo" || ev["sender_id"] != "2" {
		t.Fatalf("message 事件不对: %v", ev)
	}
}

func TestOriWsRawFrame(t *testing.T) {
	g, _ := newTestGW()
	ts := httptest.NewServer(g.handler())
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/oriws?access_token=tok123"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var hello map[string]any
	if err := c.ReadJSON(&hello); err != nil || hello["channel"] != "raw" {
		t.Fatalf("首帧应为 raw hello: %v", hello)
	}
	payload := []byte{0x08, 0x64} // protobuf: field1 varint 100
	g.EmitRaw(payload)
	var ev map[string]any
	if err := c.ReadJSON(&ev); err != nil {
		t.Fatal(err)
	}
	if ev["type"] != "raw" || ev["b64"] != base64.StdEncoding.EncodeToString(payload) {
		t.Fatalf("raw 事件不对: %v", ev)
	}
	fields, _ := ev["fields"].([]any)
	if len(fields) != 1 {
		t.Fatalf("应解出 1 个字段: %v", fields)
	}
	f0 := fields[0].(map[string]any)
	if f0["f"].(float64) != 1 || f0["t"] != "varint" || f0["v"] != "100" {
		t.Fatalf("f1 应为 varint 100: %v", f0)
	}
}

func TestWebSocketBadToken(t *testing.T) {
	g, _ := newTestGW()
	ts := httptest.NewServer(g.handler())
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?access_token=bad"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return // 直接被拒也可接受
	}
	defer c.Close()
	var m map[string]any
	if err := c.ReadJSON(&m); err != nil || m["type"] != "error" {
		t.Fatalf("错令牌应收到 error 帧: %v %v", m, err)
	}
}
