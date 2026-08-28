package engine

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 电脑版(douyin_pc / imapi) 走 HTTP，body 是 application/x-protobuf（与 WS 帧同构，去掉 frontier 外壳）。
// 无公开 .proto，直接用裸 proto 编码器(proto.go)按 HAR 硬拼。所有动作共用同一外壳，只有 cmd 号 + f8 内层不同。
const (
	imapiSendURL   = "https://imapi.douyin.com/v1/message/send"
	imapiRecallURL = "https://imapi.douyin.com/v1/message/recall"
	imapiPropURL   = "https://imapi.douyin.com/v1/message/set_property"
	webSDKVersion  = "0.1.8"
	webBuildNumber = "0d50935:feat/pc-im-group"
	webSessionAID  = "339757"
	webAppName     = "douyin_pc"
	webBiz         = "douyin_im_pc"
	webAccess      = "web_sdk"
	pcUA           = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) douyinim/1.1.33 Chrome/136.0.7103.59 Electron/36.3.2 Safari/537.36"
)

// 复用 http.Client 以复用连接（TLS 握手）。imHTTP 用于短请求，uploadHTTP 用于上传/分片。
var (
	imHTTP     = &http.Client{Timeout: 30 * time.Second}
	uploadHTTP = &http.Client{Timeout: 3 * time.Minute}
)

// imapiTextContent 文本 content JSON（字段顺序照 HAR：aweType,type,richTextInfos,text）。
type imapiTextContent struct {
	AweType       int    `json:"aweType"`
	Type          int    `json:"type"`
	RichTextInfos []any  `json:"richTextInfos"`
	Text          string `json:"text"`
}

// ImageAsset upload_image 的产物，喂给发图。
type ImageAsset struct {
	Oid         string `json:"oid"`
	Skey        string `json:"skey"`
	DataSize    int    `json:"data_size"`
	Md5         string `json:"md5"`
	CoverWidth  int    `json:"cover_width"`
	CoverHeight int    `json:"cover_height"`
}

// imapiImageContent 图片 content JSON（字段顺序照 HAR）。
type imapiImageContent struct {
	ResourceURL struct {
		Oid      string `json:"oid"`
		Skey     string `json:"skey"`
		DataSize int    `json:"data_size"`
		Md5      string `json:"md5"`
	} `json:"resource_url"`
	CoverHeight int    `json:"cover_height"`
	CoverWidth  int    `json:"cover_width"`
	CheckPics   []any  `json:"check_pics"`
	Md5         string `json:"md5"`
	FromGallery int    `json:"from_gallery"`
	AweType     int    `json:"aweType"`
}

func (c *Client) deviceID() string {
	if strings.TrimSpace(c.DeviceID) != "" {
		return c.DeviceID
	}
	return c.CkUid // 兜底
}

// fingerprintKVs f15 里的浏览器指纹 KV（session_did 用给定 device_id）。
func fingerprintKVs(dev string) [][2]string {
	return [][2]string{
		{"session_aid", webSessionAID}, {"session_did", dev}, {"app_name", webAppName},
		{"priority_region", "cn"}, {"user_agent", pcUA}, {"cookie_enabled", "true"},
		{"browser_language", "zh-CN"}, {"browser_platform", "Win32"}, {"browser_name", "Mozilla"},
		{"browser_version", strings.TrimPrefix(pcUA, "Mozilla/")}, {"browser_online", "true"},
		{"screen_width", "1707"}, {"screen_height", "1067"}, {"referer", ""},
		{"timezone_name", "Asia/Shanghai"}, {"is-retry", "0"},
	}
}

// buildEnvelope imapi 通用外壳：cmd + f8(内层 fieldNum→inner) + 指纹。所有动作共用。
func (c *Client) buildEnvelope(cmd, innerField int, inner []byte, dev string) []byte {
	body := concat(
		encodeFieldVarint(1, uint64(cmd)),
		encodeFieldVarint(2, uint64(c.nextSeq())),
		encodeLenDelimS(3, webSDKVersion),
		encodeLenDelimS(4, ""),
		encodeFieldVarint(5, 3),
		encodeFieldVarint(6, 0),
		encodeLenDelimS(7, webBuildNumber),
		encodeLenDelim(8, encodeLenDelim(innerField, inner)),
		encodeLenDelimS(9, dev),
		encodeLenDelimS(11, webAppName),
		encodeLenDelimS(14, "360000"),
	)
	for _, kv := range fingerprintKVs(dev) {
		body = append(body, encodeKvPair(15, kv[0], kv[1])...)
	}
	body = append(body, encodeFieldVarint(18, 1)...)
	body = append(body, encodeLenDelimS(21, webBiz)...)
	body = append(body, encodeLenDelimS(22, webAccess)...)
	return body
}

// buildIMAPIBody 组发消息(cmd100)的 body，返回 (body, clientMsgID)。
func (c *Client) buildIMAPIBody(convID string, shortID uint64, contentJSON string) ([]byte, string) {
	ms := time.Now().UnixMilli()
	clientMsgID := uuid.NewString()
	field100 := concat(
		encodeLenDelimS(1, convID),
		encodeFieldVarint(2, 1),
		encodeFieldVarint(3, shortID),
		encodeLenDelimS(4, contentJSON),
		encodeKvPair(5, "s:mentioned_users", ""),
		encodeKvPair(5, "s:client_message_id", clientMsgID),
		encodeKvPair(5, "s:stime", fmt.Sprintf("%d.%04d", ms, ms%10000)),
		encodeFieldVarint(6, 7),
		encodeLenDelimS(8, clientMsgID),
	)
	return c.buildEnvelope(100, 100, field100, c.deviceID()), clientMsgID
}

// postIMAPIRaw POST 一段 protobuf 到 imapi，返回响应字节。
func (c *Client) postIMAPIRaw(url string, body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("User-Agent", pcUA)
	req.Header.Set("Cookie", c.Cookie)
	req.Header.Set("Referer", "https://imdesktop.douyin.com")
	resp, err := imHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return rb, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(rb))
	}
	return rb, nil
}

// sendIMAPI 发消息(cmd100)并解析回执（f3=status,0=OK；f6→f100→f1=server_msg_id）。
func (c *Client) sendIMAPI(convID string, shortID uint64, contentJSON string) (SendResult, error) {
	shortID = c.resolveShort(convID, shortID)
	res := SendResult{ConvID: convID, SelfUID: c.CkUid, ConvShortID: u64str(shortID)}
	if strings.TrimSpace(c.CkUid) == "" {
		return res, fmt.Errorf("未初始化：缺少 user_id")
	}
	body, clientMsgID := c.buildIMAPIBody(convID, shortID, contentJSON)
	res.ClientMsgID = clientMsgID
	rb, err := c.postIMAPIRaw(imapiSendURL, body)
	if err != nil {
		return res, err
	}
	top := decodeTop(rb)
	if status, _ := searchPath(top, []int{3}); status != 0 {
		return res, fmt.Errorf("发送失败 status=%d: %s", status, snippet(rb))
	}
	if smid, ok := searchPath(top, []int{6, 100, 1}); ok {
		res.ServerMsgID = strconv.FormatUint(smid, 10)
	}
	return res, nil
}

func u64str(v uint64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatUint(v, 10)
}

func snippet(b []byte) string {
	if len(b) > 160 {
		return string(b[:160])
	}
	return string(b)
}
