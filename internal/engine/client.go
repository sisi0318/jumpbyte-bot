package engine

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// -- 常量（来自 ImClient.ts）------------------------------------------------

const (
	androidSendWsBase = "wss://frontier-aweme-lf-ipainner.amemv.com/ws/v2"

	akFpID   = "9"
	akAppKey = "e1bd35ec9db7b8d846de66ed140b1ad9"
	akSalt   = "f8a69f1719916z"

	awemeAID               = "1128"
	awemeVersionCode       = "280400"
	awemeVersionName       = "28.4.0"
	awemeUpdateVersionCode = "28409900"
	awemeChannel           = "douyinweb1_64"
	awemeDeviceType        = "24031PN0DC"
	awemeDeviceBrand       = "XIAOMI"
	awemeOSVersion         = "14"
	awemeOSAPI             = "34"
	awemeAppName           = "aweme"
	awemeAppPackage        = "com.ss.android.ugc.aweme"
	awemeUA                = "okhttp/3.12.1 com.ss.android.ugc.aweme/280400"

	// WS 发送(安卓 frontier cmd100 内层)专用，逆自 TS ImClient.buildAndroidCmd100Inner。
	awemeSDKVersion   = "5.0.3.0-rc.11-SNAPSHOT"
	awemeBuildNumber2 = "5030"
)

// -- 对外类型 ---------------------------------------------------------------

// ImImage 图片消息（aweType 2702）资源，供下载 + AES-256-GCM(key=skey) 解密。
type ImImage struct {
	Skey          string
	Oid           string
	Md5           string
	DataSize      int
	CoverWidth    int
	CoverHeight   int
	OriginURLList []string
	LargeURLList  []string
	MediumURLList []string
	ThumbURLList  []string
}

// PickURL 取一个可用的原图 URL（优先 origin，其次 large/medium/thumb）。
func (im *ImImage) PickURL() string {
	for _, l := range [][]string{im.OriginURLList, im.LargeURLList, im.MediumURLList, im.ThumbURLList} {
		if len(l) > 0 {
			return l[0]
		}
	}
	return ""
}

// ImVideo 视频消息资源。视频流用 tkey 走 batch_play_info 换可播 URL；poster 是封面图（可解密）。
type ImVideo struct {
	Tkey      string
	Skey      string
	Md5       string
	Width     int
	Height    int
	CheckPics []string
	Poster    *ImImage
}

// ImEmoji 表情消息（aweType 507）。url 是明文图地址（im-resource），可直接展示，无需解密。
type ImEmoji struct {
	DisplayName string
	ImageType   string
	Width       int
	Height      int
	URL         string
	StickerID   string
}

// IncomingMessage 投递给上层的一条收到的消息。
type IncomingMessage struct {
	ConvID    string
	IsGroup   bool // 群聊(conv_id 为纯数字)则 true；私信(0:1:小:大)为 false
	SenderID  string
	SenderMs4 string
	Text      string
	AweType   int
	Image     *ImImage
	Video     *ImVideo
	Emoji     *ImEmoji
	Direction string // 目前只投递 recv
}

// Client frontier-aweme IM 客户端（单账号）。
type Client struct {
	Cookie      string
	CkUid       string // 账号 user_id，既作 selfUid 也作 device_id
	DeviceID    string
	Proxy       *WsProxy
	SendChannel string       // 发送通道："ws" 走安卓 frontier WS，其余(默认)走 HTTP imapi
	OnRaw       func([]byte) // 可选：每收到一帧原始 payload 就回调（调试用）
	seq         int64
	shortIDs    sync.Map // convID(string) -> conv_short_id(uint64)，从收到的消息里学，发送时回填
}

// resolveShort 发送时若未显式给 short_id，用收包学到的缓存回填。
func (c *Client) resolveShort(convID string, shortID uint64) uint64 {
	if shortID != 0 {
		return shortID
	}
	if v, ok := c.shortIDs.Load(convID); ok {
		return v.(uint64)
	}
	return 0
}

// New 创建客户端。uid 为账号 user_id。
func New(cookie, uid, deviceID string) *Client {
	return &Client{Cookie: cookie, CkUid: strings.TrimSpace(uid), DeviceID: deviceID}
}

func (c *Client) nextSeq() int { return int(atomic.AddInt64(&c.seq, 1)) }

func (c *Client) computeAccessKey(deviceID string) string {
	sum := md5.Sum([]byte(akFpID + akAppKey + deviceID + akSalt))
	return hex.EncodeToString(sum[:])
}

// -- 连接 -------------------------------------------------------------------

// Connect 建立 frontier-aweme 发送/接收连接。
func (c *Client) Connect() (*WsConn, error) {
	if strings.TrimSpace(c.CkUid) == "" {
		return nil, errors.New("发送连接失败：缺少 user_id")
	}
	return c.makeClient(c.buildAndroidSendWsURL())
}

func (c *Client) buildAndroidSendWsURL() string {
	deviceID := c.CkUid
	now := time.Now().Unix()
	params := [][2]string{
		{"aid", awemeAID}, {"fpid", akFpID}, {"sdk_version", "3"}, {"device_id", deviceID}, {"iid", deviceID},
		{"access_key", c.computeAccessKey(deviceID)}, {"pl", "0"}, {"ne", "1"},
		{"version_code", awemeVersionCode}, {"version_name", awemeVersionName}, {"update_version_code", awemeUpdateVersionCode},
		{"platform", "0"}, {"monitor_service_id_list", "[]"}, {"is_background", "0"}, {"ping-interval", "30"},
		{"qos_level", "2"}, {"qos_sdk_version", "2"}, {"ttnet_ignore_offline", "1"}, {"ws_connect_protocol", "0"},
		{"device_platform", "android"}, {"os", "android"}, {"app_name", awemeAppName}, {"package", awemeAppPackage},
		{"channel", awemeChannel}, {"ac", "wifi"}, {"language", "zh"}, {"device_type", awemeDeviceType},
		{"device_brand", awemeDeviceBrand}, {"os_api", awemeOSAPI}, {"os_version", awemeOSVersion},
		{"ts", strconv.FormatInt(now, 10)}, {"_rticket", strconv.FormatInt(now*1000, 10)},
	}
	var sb strings.Builder
	for i, kv := range params {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(kv[0])
		sb.WriteByte('=')
		sb.WriteString(rawURLEncode(kv[1]))
	}
	return androidSendWsBase + "?" + sb.String()
}

func (c *Client) makeClient(wsURL string) (*WsConn, error) {
	headers := map[string]string{
		"User-Agent":             awemeUA,
		"Origin":                 "wss://frontier-aweme-lf-ipainner.amemv.com",
		"Cookie":                 c.Cookie,
		"x-support-qos2":         "1",
		"x-support-ack":          "1",
		"sdk-version":            "2",
		"passport-sdk-version":   "601504",
		"X-SS-DP":                awemeAID,
		"x-tt-store-region":      "cn",
		"x-tt-store-region-src":  "uid",
		"x-bd-kmsv":              "1",
		"Sec-WebSocket-Protocol": "pbbp2",
	}
	cookies := parseCookies(c.Cookie)
	if v, ok := cookies["session_tlb_tag"]; ok {
		headers["session-tlb-tag"] = uriDecode(v)
	}
	if v, ok := cookies["passport_mfa_token"]; ok {
		headers["x-tt-passport-mfa-token"] = uriDecode(v)
	}
	return wsConnect(wsURL, headers, c.Proxy, 30*time.Second)
}

// -- 收发主循环 -------------------------------------------------------------

// RunSession 心跳 + 收包主循环。
// 返回 reason（stop / disconnect，网关事件用，值域不变）和 detail（人类可读的断开原因，打日志用）。
func (c *Client) RunSession(conn *WsConn, onMessage func(IncomingMessage), shouldStop func() bool) (string, string) {
	// 连接参数里声明了 ping-interval=30，我们 15 秒一个 ping 留足余量。
	// deadAfter 判的是「任何帧都没收到」——pong 也算在内，所以只有真的黑洞掉线才会触发，
	// 阈值给宽松些：宁可晚几十秒发现，也别把好连接自己掐掉重连。
	const heartbeat = 15 * time.Second
	const deadAfter = 90 * time.Second

	var stopped atomic.Bool
	var detail atomic.Pointer[string]
	setDetail := func(s string) { detail.CompareAndSwap(nil, &s) } // 只记第一个原因

	stop := make(chan struct{})
	var once sync.Once
	done := func() { once.Do(func() { close(stop) }) }

	go func() {
		t := time.NewTicker(heartbeat)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				conn.Ping()
				if idle := conn.IdleFor(); idle > deadAfter {
					setDetail(fmt.Sprintf("本地判死：%s 没收到任何帧（含 pong）", idle.Round(time.Second)))
					done()
					return
				}
			}
		}
	}()

	if shouldStop != nil {
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					if shouldStop() {
						stopped.Store(true)
						done()
						return
					}
				}
			}
		}()
	}

loop:
	for {
		select {
		case <-stop:
			break loop
		default:
		}
		raw, err := conn.Receive(time.Second)
		if err != nil {
			setDetail("连接被关闭：" + err.Error())
			break
		}
		if len(raw) == 0 {
			continue
		}
		if c.OnRaw != nil {
			c.OnRaw(raw)
		}
		c.handleIncoming(raw, onMessage)
	}
	done()
	conn.Close()

	if stopped.Load() {
		return "stop", ""
	}
	if d := detail.Load(); d != nil {
		return "disconnect", *d
	}
	if e := conn.Err(); e != nil {
		return "disconnect", e.Error()
	}
	return "disconnect", ""
}

func (c *Client) handleIncoming(raw []byte, onMessage func(IncomingMessage)) {
	items := c.extractChatItems(raw, c.CkUid)
	if len(items) == 0 || onMessage == nil {
		return
	}
	self := strings.TrimSpace(c.CkUid)
	for _, it := range items {
		if it.shortID != 0 && it.convID != "" {
			c.shortIDs.Store(it.convID, it.shortID) // 学会话短 ID，供发送回填
		}
		// 投递全部（recv + sent，带 Direction），由消费侧决定是否要自己发的
		convID := it.convID
		if convID == "" && !strings.HasPrefix(it.text, "[系统]") {
			peer := strings.TrimSpace(it.senderID)
			if isDigits(self) && isDigits(peer) {
				if self == peer {
					convID = "0:1:" + self + ":" + self // 自聊
				} else {
					x, y := self, peer
					if convLess(y, x) {
						x, y = y, x
					}
					convID = "0:1:" + x + ":" + y
				}
			}
		}
		onMessage(IncomingMessage{
			ConvID: convID, IsGroup: isGroupConv(convID), SenderID: it.senderID, SenderMs4: it.senderMs4,
			Text: it.text, AweType: it.aweType, Image: it.image, Video: it.video, Emoji: it.emoji, Direction: it.direction,
		})
	}
}

// BuildConvID 由自己和对方 uid 拼单聊 conv_id（0:1:小:大，按 TS 排序规则）。非法返回 ""。
func BuildConvID(selfUID, peerUID string) string {
	self := strings.TrimSpace(selfUID)
	peer := strings.TrimSpace(peerUID)
	if !isDigits(self) || !isDigits(peer) || self == peer {
		return ""
	}
	x, y := self, peer
	if convLess(y, x) {
		x, y = y, x
	}
	return "0:1:" + x + ":" + y
}

// convLess 复刻 TS 排序：先按长度升序，再字典序。
func convLess(p, q string) bool {
	if len(p) != len(q) {
		return len(p) < len(q)
	}
	return p < q
}

// -- 聊天条目提取（collectChat / parseChatJsonItem）-------------------------

type chatItem struct {
	direction   string
	convID      string
	shortID     uint64
	serverMsgID uint64
	senderID    string
	senderMs4   string
	text        string
	aweType     int
	image       *ImImage
	video       *ImVideo
	emoji       *ImEmoji
}

type parsedItem struct {
	text      string
	aweType   int
	direction string
	image     *ImImage
	video     *ImVideo
	emoji     *ImEmoji
}

func (c *Client) extractChatItems(payload []byte, selfUid string) []chatItem {
	var out []chatItem
	c.collectChat(decodeTop(payload), &out, "", "", "", selfUid)
	if len(out) == 0 {
		c.collectChatFromRawJson(payload, &out, selfUid)
	}
	return out
}

func (c *Client) collectChat(fields []ProtoField, out *[]chatItem, senderID, convID, senderMs4, selfUid string) {
	// pass 1：拿发送者 / 会话 / sec_uid 上下文
	for _, f := range fields {
		if f.Field == 7 && f.Type == "varint" {
			senderID = strconv.FormatUint(f.VUint, 10)
		}
		if f.Field == 7 && f.Type == "string" && isDigits(f.VStr) {
			senderID = f.VStr
		}
		if f.Type == "string" {
			v := f.VStr
			if v != "" && strings.HasPrefix(v, "0:1:") && strings.Count(v, ":") == 3 {
				convID = v
			} else if f.Field == 1 && v != "" && convID == "" {
				convID = v
			}
		}
		if f.Field == 14 && f.Type == "string" && strings.HasPrefix(f.VStr, "MS4") {
			senderMs4 = f.VStr
		}
	}

	// pass 2：抠 JSON 条目
	for _, f := range fields {
		if f.Type == "string" {
			fid := f.Field
			val := f.VStr
			shouldTry := fid == 6 || fid == 8
			if !shouldTry && (strings.Contains(val, `"text"`) || strings.Contains(val, "aweType") || strings.Contains(val, "tkey")) {
				shouldTry = true
			}
			if shouldTry {
				var obj map[string]any
				if json.Unmarshal([]byte(val), &obj) == nil && obj != nil {
					if p, ok := c.parseChatJsonItem(obj, senderID, selfUid); ok {
						*out = append(*out, chatItem{
							direction: p.direction, convID: convID, senderID: senderID,
							senderMs4: senderMs4, text: p.text, aweType: p.aweType,
							image: p.image, video: p.video, emoji: p.emoji,
						})
					}
				}
			}
		}
		if f.Type == "message" {
			c.collectChat(f.VMsg, out, senderID, convID, senderMs4, selfUid)
		}
	}

	var shortID uint64
	for _, path := range [][]int{{8, 6, 500, 5, 5}, {8, 6, 100, 10, 2}, {8, 6, 100, 50, 2}} {
		if v, ok := searchPath(fields, path); ok {
			shortID = v
			break
		}
	}
	serverMsgID, _ := searchPath(fields, []int{8, 6, 500, 5, 3})
	for i := range *out {
		if shortID != 0 && (*out)[i].shortID == 0 {
			(*out)[i].shortID = shortID
		}
		if serverMsgID != 0 && (*out)[i].serverMsgID == 0 {
			(*out)[i].serverMsgID = serverMsgID
		}
	}
}

var reConvID = regexp.MustCompile(`0:1:\d+:\d+`)
var reJSONChunk = regexp.MustCompile(`\{(?:[^{}]|\{[^{}]*\})*\}`)

// collectChatFromRawJson protobuf 解不出条目时的兜底：直接从裸字节抠 JSON 片段。
func (c *Client) collectChatFromRawJson(payload []byte, out *[]chatItem, selfUid string) {
	text := string(payload)
	convID := reConvID.FindString(text)

	seen := map[string]bool{}
	for _, chunk := range reJSONChunk.FindAllString(text, -1) {
		if !strings.Contains(chunk, `"text"`) && !strings.Contains(chunk, "aweType") && !strings.Contains(chunk, "tkey") {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(chunk), &obj) != nil || obj == nil {
			continue
		}
		p, ok := c.parseChatJsonItem(obj, "", selfUid)
		if !ok {
			continue
		}
		key := p.text + "|" + strconv.Itoa(p.aweType)
		if seen[key] {
			continue
		}
		seen[key] = true
		*out = append(*out, chatItem{
			direction: p.direction, convID: convID, text: p.text, aweType: p.aweType,
			image: p.image, video: p.video, emoji: p.emoji,
		})
	}
}

// parseImageRes 从 resource_url / poster 风格的 map 抠出 ImImage（cover_w/h 从 outer 取）。
func parseImageRes(ru, outer map[string]any) *ImImage {
	skey, _ := ru["skey"].(string)
	if skey == "" {
		return nil
	}
	oid, _ := ru["oid"].(string)
	md5s, _ := ru["md5"].(string)
	return &ImImage{
		Skey: skey, Oid: oid, Md5: md5s,
		DataSize:      toInt(ru["data_size"]),
		CoverWidth:    toInt(outer["cover_width"]),
		CoverHeight:   toInt(outer["cover_height"]),
		OriginURLList: strList(ru["origin_url_list"]),
		LargeURLList:  strList(ru["large_url_list"]),
		MediumURLList: strList(ru["medium_url_list"]),
		ThumbURLList:  strList(ru["thumb_url_list"]),
	}
}

func (c *Client) parseChatJsonItem(obj map[string]any, senderID, selfUid string) (parsedItem, bool) {
	aweType := toInt(obj["aweType"])

	// 先抠图片/视频：这类消息往往没有 text 字段，必须在"文本判空"之前处理，否则会被丢掉。
	var image *ImImage
	if aweType == 2702 {
		if ru, ok := obj["resource_url"].(map[string]any); ok {
			image = parseImageRes(ru, obj)
		}
	}

	// 视频：content 里没有 aweType，靠 video.tkey 识别；poster 是封面图（可解密）。
	var video *ImVideo
	if v, ok := obj["video"].(map[string]any); ok {
		if tkey, _ := v["tkey"].(string); tkey != "" {
			skey, _ := v["skey"].(string)
			md5s, _ := v["md5"].(string)
			video = &ImVideo{
				Tkey: tkey, Skey: skey, Md5: md5s,
				Width: toInt(obj["width"]), Height: toInt(obj["height"]),
				CheckPics: strList(obj["check_pics"]),
			}
			if p, ok := obj["poster"].(map[string]any); ok {
				video.Poster = parseImageRes(p, p)
			}
		}
	}

	// 表情（aweType 507）：往往没有 text 字段，须在"文本判空"前抠出，否则被丢。url 是明文图。
	var emoji *ImEmoji
	if aweType == 507 {
		emoji = parseEmoji(obj)
	}

	// 文本：text > 展示文案 > 表情名/占位
	text, _ := obj["text"].(string)
	if text == "" {
		text = c.parseMessageDisplay(obj)
	}
	if text == "" {
		switch {
		case video != nil:
			text = "[视频]"
		case image != nil:
			text = "[图片]"
		case emoji != nil:
			if emoji.DisplayName != "" {
				text = emoji.DisplayName
			} else {
				text = "[表情]"
			}
		}
	}
	if text == "" {
		return parsedItem{}, false
	}

	sid := strings.TrimSpace(senderID)
	self := strings.TrimSpace(selfUid)
	if self == "" {
		self = strings.TrimSpace(c.CkUid)
	}
	dir := "recv"
	if self != "" && sid != "" && sid == self {
		dir = "sent"
	}
	return parsedItem{text: text, aweType: aweType, direction: dir, image: image, video: video, emoji: emoji}, true
}

// parseEmoji 从表情 content 抠出展示信息。url 优先取 url_list[0]，其次 uri。
func parseEmoji(obj map[string]any) *ImEmoji {
	e := &ImEmoji{
		DisplayName: toStr(obj["display_name"]),
		ImageType:   toStr(obj["image_type"]),
		Width:       toInt(obj["width"]),
		Height:      toInt(obj["height"]),
		StickerID:   toStr(obj["sticker_id"]),
	}
	if u, ok := obj["url"].(map[string]any); ok {
		if list := strList(u["url_list"]); len(list) > 0 {
			e.URL = list[0]
		} else if uri, _ := u["uri"].(string); uri != "" {
			e.URL = uri
		}
	}
	return e
}

// parseMessageDisplay 各类消息体的展示文案：文本 > 富文本 > 卡片标题 > 提示语。
func (c *Client) parseMessageDisplay(obj map[string]any) string {
	if t, ok := obj["text"].(string); ok && t != "" {
		return t
	}
	if content, ok := obj["content"].(string); ok {
		body := strings.TrimSpace(strings.ReplaceAll(content, "{{web_url}}", ""))
		if body != "" {
			return body
		}
	}
	if title, ok := obj["title"]; ok && toStr(title) != "" {
		var extra any
		for _, k := range []string{"open_url", "link_url", "desc"} {
			if v, ok := obj[k]; ok && v != nil {
				extra = v
				break
			}
		}
		s := "[卡片] " + toStr(title)
		if e := toStr(extra); e != "" {
			s += " | " + e
		}
		return s
	}
	for _, key := range []string{"push_detail", "desc", "hint", "msgHint"} {
		if v, ok := obj[key]; ok && toStr(v) != "" {
			return toStr(v)
		}
	}
	if sm, ok := obj["status_msg"].(map[string]any); ok {
		if mc, ok := sm["msg_content"].(map[string]any); ok {
			if tips, ok := mc["tips"]; ok && toStr(tips) != "" {
				return "[系统] " + toStr(tips)
			}
		}
	}
	if tips, ok := obj["tips"]; ok && toStr(tips) != "" {
		return "[系统] " + toStr(tips)
	}
	return ""
}

// SendResult 发送返回（对应网关 API 的 data）。
type SendResult struct {
	ClientMsgID string `json:"client_msg_id"`
	ServerMsgID string `json:"server_msg_id"`
	PrevMsgID   string `json:"prev_msg_id"`
	ConvID      string `json:"conv_id"`
	ConvShortID string `json:"conversation_short_id"`
	SelfUID     string `json:"self_uid"`
}

// SendTextResult 发 HTTP 文本消息（imapi /v1/message/send），返回 client_msg_id / server_msg_id 等。
func (c *Client) SendTextResult(conversationID, text string) (SendResult, error) {
	return c.SendTextEx(conversationID, 0, text)
}

// SendTextEx 发文本，可带会话短 ID（回复已知会话时带上更稳；0 表示不带）。
func (c *Client) SendTextEx(conversationID string, shortID uint64, text string) (SendResult, error) {
	content := jsonNoEscape(imapiTextContent{AweType: 700, Type: 0, RichTextInfos: []any{}, Text: text})
	return c.dispatchSend(conversationID, shortID, content, msgTypeText)
}

// SendImageResult 发图片（先 UploadImage 拿 ImageAsset）。走同一个 imapi 发送通道，只是 content 不同。
func (c *Client) SendImageResult(conversationID string, shortID uint64, img ImageAsset) (SendResult, error) {
	var content imapiImageContent
	content.ResourceURL.Oid = img.Oid
	content.ResourceURL.Skey = img.Skey
	content.ResourceURL.DataSize = img.DataSize
	content.ResourceURL.Md5 = img.Md5
	content.CoverHeight = img.CoverHeight
	content.CoverWidth = img.CoverWidth
	content.CheckPics = []any{}
	content.Md5 = img.Md5
	content.FromGallery = 1
	content.AweType = 2702
	return c.dispatchSend(conversationID, shortID, jsonNoEscape(content), msgTypeImage)
}

// SendText 发一条文本。
func (c *Client) SendText(conversationID, text string) error {
	_, err := c.SendTextResult(conversationID, text)
	return err
}

// -- 小工具 -----------------------------------------------------------------

func rawURLEncode(s string) string {
	const hexd = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '_' || ch == '.' || ch == '~' {
			b.WriteByte(ch)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexd[ch>>4])
			b.WriteByte(hexd[ch&0xf])
		}
	}
	return b.String()
}

func jsonNoEscape(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimRight(buf.String(), "\n")
}

func parseCookies(cookie string) map[string]string {
	m := map[string]string{}
	for _, part := range strings.Split(cookie, ";") {
		p := strings.TrimSpace(part)
		if p == "" || !strings.Contains(p, "=") {
			continue
		}
		i := strings.Index(p, "=")
		m[strings.TrimSpace(p[:i])] = p[i+1:]
	}
	return m
}

func uriDecode(s string) string {
	if d, err := url.PathUnescape(s); err == nil {
		return d
	}
	return s
}

func strList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
