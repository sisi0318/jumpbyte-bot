# jumpbyte-bot

## 功能

- 扫码登录（含短信二次验证），cookie 失效自动重新登录
- 收私信：文本、图片（自动解密）
- 发私信：文本、图片、视频、表情、引用回复
- 撤回消息
- HTTP 接口发消息 + WebSocket 推事件（见 [API.md](API.md)）
- 原始 protobuf 帧调试通道 `/oriws`

## 构建

```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o dist/jumpbyte-bot ./cmd/bot
```

## 命令

```
jumpbyte-bot              探测 cookie（失效自动扫码登录）→ 连接 IM → 启动网关
jumpbyte-bot login        扫码登录，写 cookie.json
jumpbyte-bot --selftest   自检算法
jumpbyte-bot --smoke      给自己发一条测试消息，验证收发联机
```

首次运行无 `cookie.json` 时自动进入扫码登录，终端打印二维码并存一份 `qrcode.png`。
二维码默认半块渲染，设 `GOBOT_QR=braille` 切换为更小的盲文点阵。

## 配置

`cookie.json`（账号，`login` 自动写入）：

```json
{
  "id": "main",
  "name": "主号",
  "cookie": "sessionid=...; ...",
  "uid": "1234567890",
  "device_id": "3249781169",
  "proxy": "",
  "channel": 1,
  "enabled": true
}
```

`bot.json`（网关，首次启动自动生成，`token` 随机）：

```json
{
  "host": "127.0.0.1",
  "port": 9503,
  "token": "<自动生成>",
  "queue_limit": 1000,
  "emit_self": false
}
```

## 网关

启动后监听 `bot.json` 里的 `host:port`（默认 `127.0.0.1:9503`）：

| 端点 | 用途 |
| --- | --- |
| `POST /api/{动作}` | 发消息 / 撤回，`Authorization: Bearer <token>` |
| `ws /ws?access_token=<token>` | 事件流（`message` / `connect` / `disconnect`） |
| `ws /oriws?access_token=<token>` | 原始 protobuf 帧（base64 + 解码树），调试 / 逆向新消息类型 |
| `GET /health` | 存活与账号状态，免鉴权 |

动作：`send_text`、`send_image`、`upload_image`、`send_video`、`send_emoji`、`send_reply`、`recall`、`get_accounts`。字段与示例见 [API.md](API.md)。

跑起来的终端也可直接输入 `@<conv_id> <文本>` 回车发消息。

### `/oriws` 原始帧

连上后每收到一帧下推：

```json
{
  "type": "raw",
  "time": 1787735984,
  "len": 711,
  "b64": "CNiJ2ZMT...",
  "fields": [
    { "f": 1, "t": "varint", "v": "100" },
    { "f": 8, "t": "message", "v": [ { "f": 100, "t": "message", "v": [ ] } ] }
  ]
}
```

`b64` 是原始字节，`fields` 是宽松解码树（字段号 / 类型 / 值），用来对照抓包分析未支持的消息类型。

## 目录

```
cmd/bot            入口：命令分发、扫码登录、收发主循环
internal/
  sign             passport web 签名（sign / qs / xor5 / msToken）
  abogus           a_bogus（SM3 + RC4变体 + base64变体）
  login            扫码登录、MFA、cookie 探测
  qr               二维码渲染 + 存 PNG
  engine           IM 引擎
    proto.go         裸 protobuf 编解码器
    wsconn.go        WebSocket（收消息）
    client.go        连接 / 收包解析
    httpsend.go      发消息 HTTP 通道
    imactions.go     撤回 / 表情 / 回复
    upload.go        图片上传（SigV4 → TOS）
    video.go         视频分片上传
  media            图片 AES-256-GCM 解密 + 本地代理
  webapi           昵称解析（带缓存）
  store            sqlite 昵称缓存
  gateway          HTTP + WS 网关
  config           cookie.json / bot.json
```

收消息走 WebSocket，发消息走 HTTP，二者 protobuf 同源，共用一套编解码器。

## 状态

单账号。`send_card` / `send_action_card` / 点赞 / `message_self` 未实现。

## 许可证

[GPL-3.0](LICENSE)
