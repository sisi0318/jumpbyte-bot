// Package gateway bot 网关：WS 单向推事件 + HTTP POST /api/{动作} 发消息 + GET /health。
// 协议 1:1 对齐 TS 版 bot/src/gateway.ts（原生语义，不套 OneBot）。
//
//	ws://host:port/ws?access_token=令牌   连上后单向收事件（先一帧 hello）
//	POST http://host:port/api/{动作}       Authorization: Bearer 令牌
//	GET  http://host:port/health           无需令牌，看存活与账号状态
package gateway

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"gobot/internal/config"
	"gobot/internal/engine"
	"gobot/internal/media"
)

const protocolVersion = 1
const maxBody = 12 * 1024 * 1024 // 图片上限 5MB，base64 后约 6.7MB

type accountStatus struct {
	Account     string `json:"account"`
	Name        string `json:"name"`
	UID         string `json:"uid"`
	State       string `json:"state"` // offline/connecting/online/invalid/disabled
	Message     string `json:"message"`
	OnlineSince int64  `json:"online_since"`
}

// wsClient 每个 WS 连接一个带缓冲的发送队列 + 独立写 goroutine。
// 慢/半死的消费者只会撑满自己的队列（丢最旧的），绝不阻塞广播方（收包线程）。
type wsClient struct {
	conn *websocket.Conn
	out  chan []byte
	done chan struct{}
	once sync.Once
}

func newWsClient(conn *websocket.Conn, queueLimit int) *wsClient {
	if queueLimit <= 0 {
		queueLimit = 1000
	}
	c := &wsClient{conn: conn, out: make(chan []byte, queueLimit), done: make(chan struct{})}
	go c.writeLoop()
	return c
}

func (c *wsClient) writeLoop() {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-c.done:
			return
		case b := <-c.out:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if c.conn.WriteMessage(websocket.TextMessage, b) != nil {
				c.close()
				return
			}
		case <-ping.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
				c.close()
				return
			}
		}
	}
}

// push 非阻塞入队；满了先丢最旧再塞，还满就放弃这条（不拖垮广播方）。
func (c *wsClient) push(b []byte) {
	select {
	case c.out <- b:
	default:
		select {
		case <-c.out:
		default:
		}
		select {
		case c.out <- b:
		default:
		}
	}
}

func (c *wsClient) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// Sender 发送能力（*engine.Client 实现；便于测试替身）。
type Sender interface {
	SendTextEx(convID string, shortID uint64, text string) (engine.SendResult, error)
	SendImageResult(convID string, shortID uint64, img engine.ImageAsset) (engine.SendResult, error)
	SendEmojiResult(convID string, shortID uint64, e engine.EmojiSpec) (engine.SendResult, error)
	SendReplyResult(convID string, shortID uint64, r engine.ReplySpec) (engine.SendResult, error)
	SendVideoResult(convID string, shortID uint64, videoBytes, coverBytes []byte, width, height int) (engine.SendResult, error)
	Recall(convID string, shortID, serverMsgID uint64) error
	UploadImage(imageBytes []byte) (engine.ImageAsset, error)
	ResolveVideoURL(tkey string) (engine.VideoURL, error)
}

// Gateway 单账号网关。
type Gateway struct {
	cfg *config.BotConfig
	acc *config.Account
	eng Sender
	up  websocket.Upgrader
	srv *http.Server

	mu      sync.Mutex
	subs    map[*wsClient]struct{} // /ws 事件订阅者
	orisubs map[*wsClient]struct{} // /oriws 原始 protobuf 订阅者（调试用）

	stMu        sync.RWMutex
	state       string
	stateMsg    string
	onlineSince int64
}

// New 创建网关。
func New(cfg *config.BotConfig, acc *config.Account, eng Sender) *Gateway {
	return &Gateway{
		cfg: cfg, acc: acc, eng: eng,
		up:      websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		subs:    map[*wsClient]struct{}{},
		orisubs: map[*wsClient]struct{}{},
		state:   "offline", stateMsg: "offline",
	}
}

// handler 路由（Start 与测试共用）。
func (g *Gateway) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", g.handleWs)
	mux.HandleFunc("/oriws", g.handleOriWs)
	mux.HandleFunc("/health", g.handleHealth)
	mux.HandleFunc("/img", media.ImageHandler) // 图片解密代理，共用本端口
	mux.HandleFunc("/api/", g.handleAPI)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, errMsg(404, "unknown path"))
	})
	return mux
}

// Start 监听 host:port（后台）。
func (g *Gateway) Start() error {
	ln, err := net.Listen("tcp", net.JoinHostPort(g.cfg.Host, strconv.Itoa(g.cfg.Port)))
	if err != nil {
		return err
	}
	media.SetProxyBase("http://" + g.linkAddr()) // 图片链接指向本端口
	g.srv = &http.Server{Handler: g.handler()}
	go func() { _ = g.srv.Serve(ln) }()
	return nil
}

// Stop 关闭 HTTP 服务并断开所有 WS 连接（优雅退出用）。
func (g *Gateway) Stop() {
	if g.srv != nil {
		_ = g.srv.Close()
	}
	g.mu.Lock()
	for c := range g.subs {
		c.close()
	}
	for c := range g.orisubs {
		c.close()
	}
	g.mu.Unlock()
}

// linkAddr 供图片链接用的地址：host 是 0.0.0.0/空时回落到 127.0.0.1。
func (g *Gateway) linkAddr() string {
	h := g.cfg.Host
	if h == "" || h == "0.0.0.0" || h == "::" {
		h = "127.0.0.1"
	}
	return net.JoinHostPort(h, strconv.Itoa(g.cfg.Port))
}

// Addr host:port，打印用。
func (g *Gateway) Addr() string { return net.JoinHostPort(g.cfg.Host, strconv.Itoa(g.cfg.Port)) }

// -- 状态 -------------------------------------------------------------------

// SetState 设置账号连接状态（offline/connecting/invalid…）。
func (g *Gateway) SetState(state, msg string) {
	g.stMu.Lock()
	g.state, g.stateMsg = state, msg
	g.stMu.Unlock()
}

// SetOnline 标记在线并记录时间。
func (g *Gateway) SetOnline() {
	g.stMu.Lock()
	g.state, g.stateMsg, g.onlineSince = "online", "online", time.Now().Unix()
	g.stMu.Unlock()
}

func (g *Gateway) list() []accountStatus {
	g.stMu.RLock()
	st, msg, since := g.state, g.stateMsg, g.onlineSince
	g.stMu.RUnlock()
	if !g.acc.Enabled {
		st, msg = "disabled", "disabled"
	}
	return []accountStatus{{Account: g.acc.ID, Name: g.acc.Name, UID: g.acc.UID, State: st, Message: msg, OnlineSince: since}}
}

// -- 事件下推 ---------------------------------------------------------------

// EmitMessage 推一条消息。别人发来的→message；自己发的→message_self（仅 emit_self 开启时）。
func (g *Gateway) EmitMessage(m engine.IncomingMessage) {
	if m.Direction == "sent" {
		if !g.cfg.EmitSelf {
			return
		}
		g.broadcast(map[string]any{
			"type": "message_self", "id": randID(), "time": time.Now().Unix(),
			"account": g.acc.ID, "self_uid": g.acc.UID,
			"conv_id": m.ConvID, "text": m.Text,
		})
		return
	}
	ev := map[string]any{
		"type": "message", "id": randID(), "time": time.Now().Unix(),
		"account": g.acc.ID, "self_uid": g.acc.UID,
		"conv_id": m.ConvID, "sender_id": m.SenderID, "sender_sec_uid": m.SenderMs4, "text": m.Text,
	}
	if m.Image != nil {
		ev["image"] = imageEvent(m.Image)
	}
	if m.Video != nil {
		ev["video"] = videoEvent(m.Video)
	}
	g.broadcast(ev)
}

// videoEvent 视频事件对象：tkey/skey + 封面 poster（带解密链接）。可播 URL 用 get_video_url 动作换。
func videoEvent(v *engine.ImVideo) map[string]any {
	ev := map[string]any{
		"tkey": v.Tkey, "skey": v.Skey, "md5": v.Md5,
		"width": v.Width, "height": v.Height, "check_pics": v.CheckPics,
	}
	if v.Poster != nil {
		ev["poster"] = imageEvent(v.Poster)
	}
	return ev
}

// imageEvent 把图片资源摊平成事件里的 image 对象：原始各档 url + 拿来即用的解密代理链接。
func imageEvent(im *engine.ImImage) map[string]any {
	linkOf := func(list []string) string {
		if len(list) > 0 {
			return media.ImageLink(list[0], im.Skey)
		}
		return ""
	}
	primary := im.PickURL()
	return map[string]any{
		"oid": im.Oid, "skey": im.Skey, "md5": im.Md5,
		"data_size": im.DataSize, "cover_width": im.CoverWidth, "cover_height": im.CoverHeight,
		"url":             primary,                           // 主 url（origin 优先），兼容旧字段
		"link":            media.ImageLink(primary, im.Skey), // 主解密链接，兼容旧字段
		"origin_url_list": im.OriginURLList,
		"large_url_list":  im.LargeURLList,
		"medium_url_list": im.MediumURLList,
		"thumb_url_list":  im.ThumbURLList,
		"links": map[string]any{ // 各档解密代理链接
			"origin": linkOf(im.OriginURLList),
			"large":  linkOf(im.LargeURLList),
			"medium": linkOf(im.MediumURLList),
			"thumb":  linkOf(im.ThumbURLList),
		},
	}
}

// EmitConnect 推账号已连接。
func (g *Gateway) EmitConnect(reason string) {
	g.broadcast(map[string]any{"type": "connect", "id": randID(), "time": time.Now().Unix(),
		"account": g.acc.ID, "self_uid": g.acc.UID, "reason": reason})
}

// EmitDisconnect 推账号断开。
func (g *Gateway) EmitDisconnect(reason string) {
	g.broadcast(map[string]any{"type": "disconnect", "id": randID(), "time": time.Now().Unix(),
		"account": g.acc.ID, "self_uid": g.acc.UID, "reason": reason})
}

func (g *Gateway) broadcast(ev any) { g.broadcastTo(g.subs, ev) }

func (g *Gateway) broadcastTo(subs map[*wsClient]struct{}, ev any) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	g.mu.Lock()
	clients := make([]*wsClient, 0, len(subs))
	for c := range subs {
		clients = append(clients, c)
	}
	g.mu.Unlock()
	for _, c := range clients {
		c.push(b)
	}
}

// EmitRaw 把收到的原始 protobuf 帧下推给 /oriws 订阅者（base64 + 解码树）。没人订阅就不解码。
func (g *Gateway) EmitRaw(payload []byte) {
	g.mu.Lock()
	n := len(g.orisubs)
	g.mu.Unlock()
	if n == 0 {
		return
	}
	g.broadcastTo(g.orisubs, map[string]any{
		"type":   "raw",
		"time":   time.Now().Unix(),
		"len":    len(payload),
		"b64":    base64.StdEncoding.EncodeToString(payload),
		"fields": engine.DecodeToTree(payload),
	})
}

// -- WS ---------------------------------------------------------------------

func (g *Gateway) handleWs(w http.ResponseWriter, r *http.Request) {
	conn, err := g.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	if !g.tokenOK(extractToken(r)) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","msg":"token 无效"}`))
		_ = conn.Close()
		return
	}
	client := newWsClient(conn, g.cfg.QueueLimit)
	g.mu.Lock()
	g.subs[client] = struct{}{}
	g.mu.Unlock()

	hello, _ := json.Marshal(map[string]any{"type": "hello", "protocol": protocolVersion, "accounts": g.list()})
	client.push(hello)

	go func() {
		defer func() {
			g.mu.Lock()
			delete(g.subs, client)
			g.mu.Unlock()
			client.close()
		}()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if strings.Contains(string(data), "ping") {
				client.push([]byte(`{"type":"pong"}`))
			}
		}
	}()
}

// handleOriWs 原始 protobuf 调试通道：连上后收到每一帧的 base64 + 解码树。
func (g *Gateway) handleOriWs(w http.ResponseWriter, r *http.Request) {
	conn, err := g.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	if !g.tokenOK(extractToken(r)) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","msg":"token 无效"}`))
		_ = conn.Close()
		return
	}
	client := newWsClient(conn, g.cfg.QueueLimit)
	g.mu.Lock()
	g.orisubs[client] = struct{}{}
	g.mu.Unlock()
	client.push([]byte(`{"type":"hello","channel":"raw"}`))

	go func() {
		defer func() {
			g.mu.Lock()
			delete(g.orisubs, client)
			g.mu.Unlock()
			client.close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

// -- HTTP -------------------------------------------------------------------

func (g *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	bots := len(g.subs)
	g.mu.Unlock()
	writeJSON(w, 200, map[string]any{"code": 0, "data": map[string]any{
		"protocol": protocolVersion, "bots": bots, "accounts": g.list()}})
}

func (g *Gateway) handleAPI(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/")
	if !knownAction(name) {
		writeJSON(w, 404, errMsg(404, "未知动作 "+name))
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, 405, errMsg(405, "POST only"))
		return
	}
	if !g.tokenOK(extractToken(r)) {
		writeJSON(w, 401, errMsg(401, "token 无效"))
		return
	}
	input := map[string]any{}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		writeJSON(w, 400, errMsg(400, "读请求体失败："+err.Error()))
		return
	}
	if s := strings.TrimSpace(string(raw)); s != "" {
		if err := json.Unmarshal([]byte(s), &input); err != nil {
			writeJSON(w, 400, errMsg(400, "请求体不是合法 JSON："+err.Error()))
			return
		}
	}
	writeJSON(w, 200, g.dispatch(name, input))
}

func knownAction(name string) bool {
	switch name {
	case "get_accounts", "send_text", "send_card", "send_action_card",
		"send_emoji", "send_image", "send_reply", "send_video", "upload_image", "recall", "get_video_url":
		return true
	}
	return false
}

// decodeB64 解 base64（可带 data:...;base64, 前缀）。
func decodeB64(s string) ([]byte, error) {
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("空")
	}
	if strings.HasPrefix(s, "data:") {
		if i := strings.Index(s, ","); i > 0 {
			s = s[i+1:]
		}
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(s))
}

func (g *Gateway) dispatch(name string, in map[string]any) any {
	switch name {
	case "get_accounts":
		return okData(g.list())
	case "send_text":
		return g.actionSendText(in)
	case "send_image":
		return g.actionSendImage(in)
	case "upload_image":
		return g.actionUploadImage(in)
	case "send_emoji":
		return g.actionSendEmoji(in)
	case "send_reply":
		return g.actionSendReply(in)
	case "send_video":
		return g.actionSendVideo(in)
	case "recall":
		return g.actionRecall(in)
	case "get_video_url":
		return g.actionGetVideoURL(in)
	default:
		// send_card / send_action_card
		return errMsg(500, "Go 版网关暂未实现该动作："+name)
	}
}

func (g *Gateway) actionSendVideo(in map[string]any) any {
	video, err := decodeB64(getStr(in, "data"))
	if err != nil {
		return errMsg(400, "data（视频 base64）: "+err.Error())
	}
	cover, err := decodeB64(getStr(in, "cover"))
	if err != nil {
		return errMsg(400, "cover（封面图 base64，必填）: "+err.Error())
	}
	convID, short, e := g.resolveConv(in)
	if e != "" {
		return errMsg(400, e)
	}
	res, err := g.eng.SendVideoResult(convID, short, video, cover, getIntV(in, "width"), getIntV(in, "height"))
	if err != nil {
		return errMsg(500, err.Error())
	}
	return okData(res)
}

func (g *Gateway) actionSendEmoji(in map[string]any) any {
	url := getStr(in, "url")
	if url == "" {
		url = getStr(in, "uri")
	}
	if url == "" {
		return errMsg(400, "缺少 url/uri（表情图地址）")
	}
	convID, short, e := g.resolveConv(in)
	if e != "" {
		return errMsg(400, e)
	}
	res, err := g.eng.SendEmojiResult(convID, short, engine.EmojiSpec{
		DisplayName: getStr(in, "display_name"), URL: url,
		Width: getIntV(in, "width"), Height: getIntV(in, "height"),
		ImageType: getStr(in, "image_type"), PackageID: getIntV(in, "package_id"),
	})
	if err != nil {
		return errMsg(500, err.Error())
	}
	return okData(res)
}

func (g *Gateway) actionSendReply(in map[string]any) any {
	text := getStr(in, "text")
	if text == "" {
		return errMsg(400, "text 不能为空")
	}
	refUID := getStr(in, "refmsg_uid")
	if refUID == "" {
		return errMsg(400, "缺少 refmsg_uid（被回复者 uid）")
	}
	convID, short, e := g.resolveConv(in)
	if e != "" {
		return errMsg(400, e)
	}
	res, err := g.eng.SendReplyResult(convID, short, engine.ReplySpec{
		Text: text, RefUID: refUID, RefSecUID: getStr(in, "refmsg_sec_uid"),
		Nickname: getStr(in, "nickname"), RefText: getStr(in, "refmsg_text"),
	})
	if err != nil {
		return errMsg(500, err.Error())
	}
	return okData(res)
}

func (g *Gateway) actionGetVideoURL(in map[string]any) any {
	tkey := getStr(in, "tkey")
	if tkey == "" {
		return errMsg(400, "缺少 tkey（视频消息里的 video.tkey）")
	}
	u, err := g.eng.ResolveVideoURL(tkey)
	if err != nil {
		return errMsg(500, err.Error())
	}
	return okData(map[string]any{"main_url": u.MainURL, "backup_url": u.BackupURL, "expire_time": u.ExpireTime})
}

func (g *Gateway) actionRecall(in map[string]any) any {
	smid := engine.ParseUint(getStr(in, "server_msg_id"))
	if smid == 0 {
		return errMsg(400, "缺少 server_msg_id")
	}
	convID, short, e := g.resolveConv(in)
	if e != "" {
		return errMsg(400, e)
	}
	if err := g.eng.Recall(convID, short, smid); err != nil {
		return errMsg(500, err.Error())
	}
	return okData(map[string]any{"ok": true, "conv_id": convID})
}

func (g *Gateway) actionUploadImage(in map[string]any) any {
	raw, err := decodeB64(getStr(in, "data"))
	if err != nil {
		return errMsg(400, "data（图片 base64，可带 data:image/...;base64, 前缀）: "+err.Error())
	}
	asset, err := g.eng.UploadImage(raw)
	if err != nil {
		return errMsg(500, err.Error())
	}
	return okData(asset)
}

// resolveConv 解析发送目标：conv_id 或 to_uid，外加可选 conv_short_id。
func (g *Gateway) resolveConv(in map[string]any) (string, uint64, string) {
	convID := getStr(in, "conv_id")
	if convID == "" {
		if toUID := getStr(in, "to_uid"); toUID != "" {
			convID = engine.BuildConvID(g.acc.UID, toUID)
		}
	}
	if convID == "" {
		return "", 0, "缺少 conv_id 或 to_uid（to_uid 需为数字 uid）"
	}
	var short uint64
	if s := getStr(in, "conv_short_id"); s != "" {
		short, _ = strconv.ParseUint(s, 10, 64)
	}
	return convID, short, ""
}

func (g *Gateway) actionSendText(in map[string]any) any {
	text := getStr(in, "text")
	if text == "" {
		return errMsg(400, "text 不能为空")
	}
	convID, short, e := g.resolveConv(in)
	if e != "" {
		return errMsg(400, e)
	}
	res, err := g.eng.SendTextEx(convID, short, text)
	if err != nil {
		return errMsg(500, err.Error())
	}
	return okData(res)
}

func (g *Gateway) actionSendImage(in map[string]any) any {
	im, ok := in["image"].(map[string]any)
	if !ok {
		return errMsg(400, "缺少 image（upload_image 返回的对象）")
	}
	convID, short, e := g.resolveConv(in)
	if e != "" {
		return errMsg(400, e)
	}
	asset := engine.ImageAsset{
		Oid: getStr(im, "oid"), Skey: getStr(im, "skey"), Md5: getStr(im, "md5"),
		DataSize: getIntV(im, "data_size"), CoverWidth: getIntV(im, "cover_width"), CoverHeight: getIntV(im, "cover_height"),
	}
	if asset.Oid == "" || asset.Skey == "" {
		return errMsg(400, "image 缺少 oid/skey")
	}
	res, err := g.eng.SendImageResult(convID, short, asset)
	if err != nil {
		return errMsg(500, err.Error())
	}
	return okData(res)
}

// -- 工具 -------------------------------------------------------------------

func (g *Gateway) tokenOK(given string) bool {
	if g.cfg.Token == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(g.cfg.Token)) == 1
}

func extractToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(r.URL.Query().Get("access_token"))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	b, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func okData(data any) map[string]any             { return map[string]any{"code": 0, "data": data} }
func errMsg(code int, msg string) map[string]any { return map[string]any{"code": code, "msg": msg} }

func getStr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return ""
}

func getIntV(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func randID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
