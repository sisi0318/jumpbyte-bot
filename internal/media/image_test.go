package media

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

func TestDecryptImageRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	iv := make([]byte, 12)
	_, _ = rand.Read(iv)
	plain := append([]byte("RIFF\x00\x00\x00\x00WEBP"), []byte("hello douyin image")...)

	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCMWithNonceSize(block, 12)
	ct := gcm.Seal(nil, iv, plain, nil) // 密文+tag
	container := append(append([]byte{}, iv...), ct...)

	out, err := DecryptImage(container, hex.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("解密往返不一致")
	}
	if SniffExt(out) != "webp" {
		t.Fatalf("SniffExt=%s want webp", SniffExt(out))
	}
}

func TestImageLink(t *testing.T) {
	p := StartImageServer(9531)
	if p == 0 {
		t.Skip("端口占用，跳过")
	}
	link := ImageLink("https://p.douyinpic.com/x.image?sig=1", "2b08ccbd")
	if link == "" || link[:4] != "http" {
		t.Fatalf("bad link: %s", link)
	}
}
