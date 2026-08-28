package media

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

// CENC 子样本 AES-CTR 往返：CTR 对称，同一套 splice 逻辑跑两遍应还原；明文(clear)段不该变。
func TestCENCSampleRoundTrip(t *testing.T) {
	key, _ := hex.DecodeString("4bedadda440db36856379936270ec6cc") // 16 字节 = AES-128
	iv := make([]byte, 8)
	_, _ = rand.Read(iv)

	plain := make([]byte, 200)
	_, _ = rand.Read(plain)
	subs := []Subsample{{Clear: 5, Protected: 16}, {Clear: 3, Protected: 100}, {Clear: 4, Protected: 72}} // 5+16+3+100+4+72=200

	enc, err := cencDecryptSample(key, iv, plain, subs) // CTR 对称，当"加密"用
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(enc, plain) {
		t.Fatal("加密后不该与明文相同")
	}
	// clear 段应保持原样
	if !bytes.Equal(enc[0:5], plain[0:5]) || !bytes.Equal(enc[21:24], plain[21:24]) {
		t.Fatal("clear 段被改动了")
	}
	dec, err := cencDecryptSample(key, iv, enc, subs)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatal("往返未还原")
	}
}

// 无子样本：整段加密的往返。
func TestCENCWholeSampleRoundTrip(t *testing.T) {
	key := make([]byte, 16)
	iv := make([]byte, 16)
	_, _ = rand.Read(key)
	_, _ = rand.Read(iv)
	plain := make([]byte, 137) // 非 16 整数倍，测 CTR 尾块
	_, _ = rand.Read(plain)

	enc, _ := cencDecryptSample(key, iv, plain, nil)
	dec, _ := cencDecryptSample(key, iv, enc, nil)
	if bytes.Equal(enc, plain) || !bytes.Equal(dec, plain) {
		t.Fatal("整段往返失败")
	}
}

// counter 跨子样本连续（不是每段重置）：把两段 protected 当成一整条解，等价于连续 CTR。
func TestCENCCounterContinuity(t *testing.T) {
	key := make([]byte, 16)
	iv := make([]byte, 8)
	_, _ = rand.Read(key)
	_, _ = rand.Read(iv)
	plain := make([]byte, 64)
	_, _ = rand.Read(plain)

	// 一整段 protected（无 clear）
	one := []Subsample{{Clear: 0, Protected: 64}}
	// 切成两段 protected，中间无 clear
	two := []Subsample{{Clear: 0, Protected: 20}, {Clear: 0, Protected: 44}}
	a, _ := cencDecryptSample(key, iv, plain, one)
	b, _ := cencDecryptSample(key, iv, plain, two)
	if !bytes.Equal(a, b) {
		t.Fatal("counter 未跨子样本连续（分段与整段结果不一致）")
	}
}
