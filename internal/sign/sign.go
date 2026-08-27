// Package sign 电脑版 passport web 签名（1:1 转译自 TS 版，已对真机 HAR 逐字节验证）。
//
//	sign = sha256( 排序后前10个 query "k=v&..." + "&" + 排序后 body "k=v&..." + "&app_key=" + AppKey )
//	qs   = xor5( 排序后前10个参数名 join(",") )
//	xor5 = 逐 UTF-8 字节 ^ 0x05 → 两位十六进制（即 code_encrypt）
package sign

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// AppKey 取自客户端 renderer（appKey:"3c452fb6..."）。
const AppKey = "3c452fb664e3de0e936108429a0bc697"

// Xor5 = code_encrypt：UTF-8 编码后每字节 ^5，两位十六进制。
// 与 JS 的手写 UTF-8(1/2/3 字节，跳过代理对)保持一致。
func Xor5(s string) string {
	var out []byte
	for _, r := range s {
		c := int(r)
		switch {
		case c <= 0x7f:
			out = append(out, byte(c))
		case c <= 0x7ff:
			out = append(out, byte(0xc0|((c>>6)&0x1f)), byte(0x80|(c&0x3f)))
		case c <= 0xffff:
			out = append(out, byte(0xe0|((c>>12)&0x0f)), byte(0x80|((c>>6)&0x3f)), byte(0x80|(c&0x3f)))
			// > 0xffff 跳过，与 JS 一致
		}
	}
	var sb strings.Builder
	sb.Grow(len(out) * 2)
	for _, b := range out {
		const hexd = "0123456789abcdef"
		v := b ^ 5
		sb.WriteByte(hexd[v>>4])
		sb.WriteByte(hexd[v&0xf])
	}
	return sb.String()
}

// CodeEncrypt 别名（短信验证码加密）。
func CodeEncrypt(s string) string { return Xor5(s) }

// sortedKV 排序 key，取前 limit 个（limit<0 表示全部），拼 "k=v&k=v"，返回 (str, keys)。
func sortedKV(m map[string]string, limit int) (string, []string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if limit >= 0 && limit < len(keys) {
		keys = keys[:limit]
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + m[k]
	}
	return strings.Join(parts, "&"), keys
}

// SignParams 计算 sign + qs。query 为完整 query（不含 sign/qs/msToken/a_bogus），body 为 POST 表单（GET 传 nil）。
func SignParams(query, body map[string]string) (signHex, qs string) {
	tStr, keys := sortedKV(query, 10)
	eStr, _ := sortedKV(body, -1)
	h := tStr + "&" + eStr + "&app_key=" + AppKey
	sum := sha256.Sum256([]byte(h))
	return hex.EncodeToString(sum[:]), Xor5(strings.Join(keys, ","))
}

const msAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// MsToken 生成 n 位随机 base64url 串（默认 128）。
func MsToken(n int) string {
	if n <= 0 {
		n = 128
	}
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = msAlphabet[b[i]&63]
	}
	return string(b)
}
