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
	SetProxyBase("")
	if got := ImageLink("https://p.douyinpic.com/x.image", "k"); got != "https://p.douyinpic.com/x.image" {
		t.Fatalf("无 base 应返回原始 url: %s", got)
	}
	SetProxyBase("http://127.0.0.1:9503/")
	got := ImageLink("https://p.douyinpic.com/x.image?sig=1", "2b08ccbd")
	want := "http://127.0.0.1:9503/img?u=" + "https%3A%2F%2Fp.douyinpic.com%2Fx.image%3Fsig%3D1" + "&k=2b08ccbd"
	if got != want {
		t.Fatalf("link 不对:\n got=%s\nwant=%s", got, want)
	}
	SetProxyBase("")
}

func TestAllowedImageHost(t *testing.T) {
	ok := []string{"https://p11-sign.douyinpic.com/x", "https://api.amemv.com/y", "https://lf3-social.iesdouyin.com/z"}
	bad := []string{"http://169.254.169.254/latest/meta-data/", "https://evil.com/x", "file:///etc/passwd", "https://douyinpic.com.evil.com/x"}
	for _, u := range ok {
		if !allowedImageHost(u) {
			t.Fatalf("应允许: %s", u)
		}
	}
	for _, u := range bad {
		if allowedImageHost(u) {
			t.Fatalf("应拒绝: %s", u)
		}
	}
}
