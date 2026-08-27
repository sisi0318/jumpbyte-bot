// Package engine IM（frontier-aweme）引擎：WebSocket 连接 + protobuf 组解包 + 消息收发。
// 1:1 转译自 TS 版 personal_ck/ImClient.ts + WsConnection.ts。
package engine

import (
	"encoding/base64"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ProtoField 宽松 protobuf 字段（imapi 无公开 .proto，按 wire type 猜）。
// Type 取值：varint / string / message / bytes。
type ProtoField struct {
	Field  int
	Type   string
	VUint  uint64       // Type==varint
	VStr   string       // Type==string
	VMsg   []ProtoField // Type==message
	VBytes []byte       // Type==bytes
}

// -- 编码 -------------------------------------------------------------------

// encodeVarint 无符号 varint。
func encodeVarint(v uint64) []byte {
	out := make([]byte, 0, 10)
	for v > 0x7f {
		out = append(out, byte(v&0x7f)|0x80)
		v >>= 7
	}
	return append(out, byte(v&0x7f))
}

func encodeTag(fieldNum, wireType int) []byte {
	return encodeVarint(uint64(fieldNum<<3) | uint64(wireType))
}

func encodeFieldVarint(fieldNum int, value uint64) []byte {
	return append(encodeTag(fieldNum, 0), encodeVarint(value)...)
}

func encodeLenDelim(fieldNum int, data []byte) []byte {
	out := encodeTag(fieldNum, 2)
	out = append(out, encodeVarint(uint64(len(data)))...)
	return append(out, data...)
}

func encodeLenDelimS(fieldNum int, s string) []byte {
	return encodeLenDelim(fieldNum, []byte(s))
}

func encodeKvPair(fieldNum int, key, value string) []byte {
	kv := append(encodeLenDelimS(1, key), encodeLenDelimS(2, value)...)
	return encodeLenDelim(fieldNum, kv)
}

func concat(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

// -- 解码 -------------------------------------------------------------------

// decodeVarintAt 从 pos 读一个 varint，返回值与新的 pos；越界/超 64bit 返回 ok=false。
func decodeVarintAt(data []byte, pos int) (uint64, int, bool) {
	var result uint64
	var shift uint
	n := len(data)
	for pos < n {
		if shift >= 64 {
			return 0, pos, false
		}
		b := data[pos]
		pos++
		result |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, pos, true
		}
		shift += 7
	}
	return result, pos, true
}

// tryUtf8 能安全当文本就返回 (s,true)；含控制字符或非法 UTF-8 返回 ("",false)。
func tryUtf8(buf []byte) (string, bool) {
	if !utf8.Valid(buf) {
		return "", false
	}
	for _, b := range buf {
		if b < 0x20 && b != 0x09 && b != 0x0a && b != 0x0d {
			return "", false
		}
	}
	return string(buf), true
}

// decodeProtobuf 宽松解码：像 JSON 的当字符串，其余尝试递归当子消息。
func decodeProtobuf(data []byte, depth int) []ProtoField {
	var fields []ProtoField
	pos := 0
	n := len(data)

	for pos < n {
		tag, np, ok := decodeVarintAt(data, pos)
		if !ok {
			break
		}
		pos = np
		fieldNum := int(tag >> 3)
		wireType := int(tag & 0x07)
		if fieldNum == 0 {
			break
		}

		switch wireType {
		case 0:
			val, np, ok := decodeVarintAt(data, pos)
			if !ok {
				return fields
			}
			pos = np
			fields = append(fields, ProtoField{Field: fieldNum, Type: "varint", VUint: val})
		case 2:
			lengthBig, np, ok := decodeVarintAt(data, pos)
			if !ok {
				return fields
			}
			pos = np
			length := int(lengthBig)
			if length < 0 || pos+length > n {
				return fields
			}
			raw := data[pos : pos+length]
			pos += length

			text, textOk := tryUtf8(raw)
			trimmed := ""
			if textOk {
				trimmed = strings.TrimLeft(text, " \t\n\r\f\v")
			}
			looksJson := textOk && trimmed != "" && (trimmed[0] == '{' || trimmed[0] == '[')

			if looksJson {
				fields = append(fields, ProtoField{Field: fieldNum, Type: "string", VStr: text})
			} else {
				var nested []ProtoField
				if depth < 8 {
					nested = decodeProtobuf(raw, depth+1)
				}
				if len(nested) > 0 {
					fields = append(fields, ProtoField{Field: fieldNum, Type: "message", VMsg: nested})
				} else if textOk {
					fields = append(fields, ProtoField{Field: fieldNum, Type: "string", VStr: text})
				} else {
					fields = append(fields, ProtoField{Field: fieldNum, Type: "bytes", VBytes: append([]byte(nil), raw...)})
				}
			}
		case 1:
			pos += 8
		case 5:
			pos += 4
		default:
			return fields
		}
	}
	return fields
}

// decodeTop 顶层解码（TS 侧有 WeakMap 缓存，Go 侧每次现解，语义一致）。
func decodeTop(payload []byte) []ProtoField {
	return decodeProtobuf(payload, 0)
}

// DecodeToTree 把原始 protobuf 解成便于 JSON 展示的树（调试 / 逆向新消息类型用）。
// 每个字段 {f: 字段号, t: 类型, v: 值}；varint 转字符串避免大数丢精度，bytes 转 base64。
func DecodeToTree(payload []byte) []map[string]any {
	return fieldsToTree(decodeTop(payload))
}

func fieldsToTree(fs []ProtoField) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		m := map[string]any{"f": f.Field, "t": f.Type}
		switch f.Type {
		case "varint":
			m["v"] = strconv.FormatUint(f.VUint, 10)
		case "string":
			m["v"] = f.VStr
		case "message":
			m["v"] = fieldsToTree(f.VMsg)
		case "bytes":
			m["v"] = base64.StdEncoding.EncodeToString(f.VBytes)
		}
		out = append(out, m)
	}
	return out
}

// searchPath 沿字段号路径找 varint，找不到返回 ok=false。
func searchPath(fields []ProtoField, path []int) (uint64, bool) {
	for _, f := range fields {
		if f.Field != path[0] {
			continue
		}
		if len(path) == 1 {
			if f.Type == "varint" {
				return f.VUint, true
			}
			return 0, false
		}
		if f.Type == "message" {
			if r, ok := searchPath(f.VMsg, path[1:]); ok {
				return r, true
			}
		}
	}
	return 0, false
}

// -- JS 兼容小工具 ----------------------------------------------------------

func itoa(n int) string { return strconv.Itoa(n) }

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// toInt 宽松取整（对应 helpers.toInt）。
func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// toStr 宽松取串（对应 helpers.toStr）。
func toStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "1"
		}
		return ""
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
