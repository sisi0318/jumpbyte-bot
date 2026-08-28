package media

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

// CENC (cenc-aes-ctr) 视频解密核心，逆自电脑版 player.js 的 decoderAESCTRData。
//
// 每个 sample：把所有 protected 子样本段拼成一条，用 key + counter(IV) 走单条 AES-128-CTR 解密，
// 再按 {clear, protected} 的顺序拼回。counter 在整条 protected 流上连续递增（跨子样本不重置）。
// key = video.skey 的原始字节（AES-128 → 16 字节）；iv = senc 的 InitializationVector（8 或 16 字节，补零到 16）。

// Subsample 一个子样本的明文/密文字节数（CENC senc 里的 BytesOfClearData / BytesOfProtectedData）。
type Subsample struct {
	Clear     int
	Protected int
}

// cencDecryptSample 解一个 sample。subs 为空表示整 sample 加密。返回同长度的明文。
func cencDecryptSample(key, iv, data []byte, subs []Subsample) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	counter := make([]byte, 16)
	copy(counter, iv) // 8 字节 IV → 高位，低 8 字节为 0；16 字节 IV → 直接用
	ctr := cipher.NewCTR(block, counter)

	// 无子样本：整段都是 protected
	if len(subs) == 0 {
		out := make([]byte, len(data))
		ctr.XORKeyStream(out, data)
		return out, nil
	}

	// 拼 protected → 单条 CTR 解 → 拼回
	var protected []byte
	pos := 0
	for _, s := range subs {
		start := pos + s.Clear
		end := start + s.Protected
		if end > len(data) {
			return nil, fmt.Errorf("子样本越界: end=%d len=%d", end, len(data))
		}
		protected = append(protected, data[start:end]...)
		pos = end
	}
	dec := make([]byte, len(protected))
	ctr.XORKeyStream(dec, protected)

	out := make([]byte, 0, len(data))
	pos, dpos := 0, 0
	for _, s := range subs {
		out = append(out, data[pos:pos+s.Clear]...)      // 明文段原样
		out = append(out, dec[dpos:dpos+s.Protected]...) // 解密后的段
		pos += s.Clear + s.Protected
		dpos += s.Protected
	}
	if pos < len(data) { // 子样本没覆盖到的尾部（一般不会有）
		out = append(out, data[pos:]...)
	}
	return out, nil
}
