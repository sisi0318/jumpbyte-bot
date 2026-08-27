# jumpbyte-bot 网关 API

协议版本 **1**。

- **收事件** —— WebSocket `ws://HOST:PORT/ws?access_token=令牌`，连上先一帧 `hello`，之后单向下推事件
- **原始帧** —— WebSocket `ws://HOST:PORT/oriws?access_token=令牌`，下推每一帧原始 protobuf（调试 / 逆向）
- **发消息 / 动作** —— HTTP `POST http://HOST:PORT/api/{动作}`
- **存活探测** —— HTTP `GET http://HOST:PORT/health`（免令牌）
- 默认地址 `127.0.0.1:9503`，令牌在 `bot.json`（首次启动自动生成）

下文示例统一用这两个变量：

```bash
BASE=http://127.0.0.1:9503
TOKEN=$(python -c "import json;print(json.load(open('bot.json'))['token'])")
```

---

## 通用约定

### 鉴权

HTTP 用请求头，WS 用 query（两者也都支持另一种）：

```
Authorization: Bearer <令牌>
?access_token=<令牌>
```

### 响应

```jsonc
{ "code": 0, "data": { … } }            // 成功
{ "code": 400, "msg": "text 不能为空" }  // 失败
```

判断成败看 `code`（鉴权失败例外，HTTP 401）。

| code | 含义 |
| --- | --- |
| `0` | 成功 |
| `400` | 参数不对 |
| `401` | 令牌无效 |
| `404` | 未知动作 / 路径 |
| `405` | 该动作只接受 POST |
| `500` | 执行失败（`msg` 里有原文） |

### 指定目标

发送类动作共用这几个字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `conv_id` | 二选一 | 会话 ID，回消息时用事件里的这个值 |
| `to_uid` | 二选一 | 目标数字 uid，网关自动拼会话 |
| `conv_short_id` | 否 | 已知就带上（回复 / 撤回更稳） |
| `account` | 否 | 单账号可省略 |

`conv_id` 格式 `0:1:{较小uid}:{较大uid}`，两个 uid 按数值排序。

### 发送类动作的返回

```jsonc
{
  "code": 0,
  "data": {
    "client_msg_id": "3f2a…",
    "server_msg_id": "7678690025631237690",
    "prev_msg_id": "",
    "conv_id": "0:1:1234:5678",
    "conversation_short_id": "",
    "self_uid": "1234"
  }
}
```

---

## 动作

### `get_accounts`

无参数。

```bash
curl -s -X POST $BASE/api/get_accounts -H "Authorization: Bearer $TOKEN"
```

```jsonc
{ "code": 0, "data": [
  { "account": "main", "name": "主号", "uid": "1234",
    "state": "online",   // offline / connecting / online / invalid / disabled
    "message": "online", "online_since": 1787735984 }
] }
```

### `send_text`

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `text` | ✅ | 消息内容 |

```bash
curl -s -X POST $BASE/api/send_text -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{"conv_id":"0:1:1234:5678","text":"你好"}'

curl -s -X POST $BASE/api/send_text -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{"to_uid":"5678","text":"你好"}'
```

### `upload_image`

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `data` | ✅ | 图片 base64，可带 `data:image/png;base64,` 前缀 |

```bash
curl -s -X POST $BASE/api/upload_image -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d "{\"data\":\"$(base64 -w0 ./photo.jpg)\"}"
```

```jsonc
{ "code": 0, "data": {
  "oid": "…", "skey": "…", "md5": "…",
  "data_size": 123456, "cover_width": 800, "cover_height": 600
} }
```

### `send_image`

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `image` | ✅ | `upload_image` 返回的整个 `data` 对象（至少含 `oid`/`skey`） |

```bash
IMG=$(curl -s -X POST $BASE/api/upload_image -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d "{\"data\":\"$(base64 -w0 ./photo.jpg)\"}" \
  | python -c "import sys,json;print(json.dumps(json.load(sys.stdin)['data']))")

curl -s -X POST $BASE/api/send_image -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d "{\"conv_id\":\"0:1:1234:5678\",\"image\":$IMG}"
```

### `send_video`

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `data` | ✅ | 视频 base64 |
| `cover` | ✅ | 封面图 base64 |
| `width` / `height` | 否 | 视频宽高，不传用封面尺寸兜底 |

```bash
curl -s -X POST $BASE/api/send_video -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d "{
    \"conv_id\":\"0:1:1234:5678\",
    \"data\":\"$(base64 -w0 video.mp4)\",
    \"cover\":\"$(base64 -w0 cover.jpg)\",
    \"width\":720, \"height\":1280
  }"
```

### `send_emoji`

| 字段 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `url`（或 `uri`） | ✅ | | 表情图地址 |
| `display_name` | 否 | 空 | 展示名，如 `[微笑]` |
| `width` / `height` | 否 | `100` | 宽 / 高 |
| `image_type` | 否 | `png` | 图片类型 |
| `package_id` | 否 | `0` | 表情包 ID |

```bash
curl -s -X POST $BASE/api/send_emoji -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
    "conv_id":"0:1:1234:5678",
    "url":"https://.../emoji.png",
    "display_name":"[微笑]"
  }'
```

### `send_reply`

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `text` | ✅ | 回复正文 |
| `refmsg_uid` | ✅ | 被回复者数字 uid（取事件里的 `sender_id`） |
| `refmsg_sec_uid` | 否 | 被回复者 sec_uid（取事件里的 `sender_sec_uid`） |
| `nickname` | 否 | 被回复者昵称 |
| `refmsg_text` | 否 | 被回复消息的原文 |

```bash
curl -s -X POST $BASE/api/send_reply -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
    "conv_id":"0:1:1234:5678", "text":"收到",
    "refmsg_uid":"5678", "refmsg_sec_uid":"MS4wLjAB…", "nickname":"张三",
    "refmsg_text":"在吗"
  }'
```

### `recall`

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `conv_id`（或 `to_uid`） | ✅ | 会话 |
| `server_msg_id` | ✅ | 发送返回里的 `server_msg_id` |
| `conv_short_id` | 否 | 已知带上更稳 |

```bash
curl -s -X POST $BASE/api/recall -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
    "conv_id":"0:1:1234:5678", "server_msg_id":"7678690025631237690"
  }'
```

返回 `{"code":0,"data":{"ok":true,"conv_id":"…"}}`。

### `GET /health`

免令牌。

```bash
curl -s $BASE/health
```

```jsonc
{ "code": 0, "data": { "protocol": 1, "bots": 1, "accounts": [ … ] } }
```

---

## 事件（WebSocket）

连上 `ws://HOST:PORT/ws?access_token=令牌`，先收到 `hello`，之后是事件流。
上行只认 `{"type":"ping"}`（回 `{"type":"pong"}`）。

### `hello`

```jsonc
{ "type": "hello", "protocol": 1, "accounts": [ … ] }
```

### `message`

```jsonc
{ "type": "message", "id": "8e3f…", "time": 1787735984,
  "account": "main", "self_uid": "1234",
  "conv_id": "0:1:1234:5678",
  "sender_id": "5678",
  "sender_sec_uid": "MS4wLjAB…",
  "text": "在吗",
  "image": {                        // 仅图片消息才有
    "url": "https://…",
    "skey": "…",
    "link": "http://127.0.0.1:9520/img?u=…&k=…"
  } }
```

拿 `conv_id` 调 `send_text` 就是回复，调 `send_reply` 就是引用回复。

### `connect` / `disconnect`

```jsonc
{ "type": "connect",    "id":"…","time":…,"account": "main", "self_uid": "1234", "reason": "online" }
{ "type": "disconnect", "id":"…","time":…,"account": "main", "self_uid": "1234", "reason": "NETWORK" }
```

`reason`：`online` / `disconnect` / `stop` / `NETWORK`。

---

## 原始帧调试（`/oriws`）

连上 `ws://HOST:PORT/oriws?access_token=令牌`，先收到 `{"type":"hello","channel":"raw"}`，
之后每收到一帧下推其原始 protobuf，用于对照抓包分析尚未支持的消息类型。

```jsonc
{
  "type": "raw",
  "time": 1787735984,
  "len": 711,
  "b64": "CNiJ2ZMT…",
  "fields": [
    { "f": 1, "t": "varint",  "v": "100" },
    { "f": 8, "t": "message", "v": [
      { "f": 100, "t": "message", "v": [
        { "f": 4, "t": "string", "v": "{\"aweType\":700,…}" }
      ] }
    ] }
  ]
}
```

`t` 取值：`varint`（`v` 是十进制字符串）/ `string` / `message`（`v` 是子树）/ `bytes`（`v` 是 base64）。

---

## 回声 bot

```bash
BASE=http://127.0.0.1:9503
TOKEN=$(python -c "import json;print(json.load(open('bot.json'))['token'])")

websocat "ws://127.0.0.1:9503/ws?access_token=$TOKEN" | while read -r line; do
  type=$(echo "$line" | python -c "import sys,json;print(json.load(sys.stdin).get('type',''))")
  [ "$type" = "message" ] || continue
  conv=$(echo "$line" | python -c "import sys,json;print(json.load(sys.stdin)['conv_id'])")
  text=$(echo "$line" | python -c "import sys,json;print(json.load(sys.stdin)['text'])")
  curl -s -X POST $BASE/api/send_text -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" -d "{\"conv_id\":\"$conv\",\"text\":\"你说的是：$text\"}"
done
```

---

未实现：`send_card` / `send_action_card`、点赞、`message_self`。
