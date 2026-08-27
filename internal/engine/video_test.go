package engine

import (
	"fmt"
	"hash/crc32"
	"strings"
	"testing"
)

// 视频 content 字段顺序应与 HAR 一致：video{tkey,md5,skey},poster{oid,md5,skey},height,width,check_pics。
func TestVideoContentShape(t *testing.T) {
	var c imapiVideoContent
	c.Video.Tkey, c.Video.Md5, c.Video.Skey = "tk", "vm", "vs"
	c.Poster.Oid, c.Poster.Md5, c.Poster.Skey = "po", "pm", "ps"
	c.Height, c.Width = 1280, 720
	c.CheckPics = []string{"cp"}
	got := jsonNoEscape(c)
	want := `{"video":{"tkey":"tk","md5":"vm","skey":"vs"},"poster":{"oid":"po","md5":"pm","skey":"ps"},"height":1280,"width":720,"check_pics":["cp"]}`
	if got != want {
		t.Fatalf("视频 content 不对:\n got=%s\nwant=%s", got, want)
	}
}

// 分片计划：12MB → 3 片(5+5+2)，part list 形如 "1:crc,2:crc,3:crc"（对齐 HAR finish body）。
func TestVideoChunkPlan(t *testing.T) {
	data := make([]byte, 12*1024*1024)
	for i := range data {
		data[i] = byte(i)
	}
	var parts []string
	n := 0
	for off := 0; off < len(data); off += videoPartSize {
		end := off + videoPartSize
		if end > len(data) {
			end = len(data)
		}
		n++
		crc := fmt.Sprintf("%08x", crc32.ChecksumIEEE(data[off:end]))
		parts = append(parts, fmt.Sprintf("%d:%s", n, crc))
	}
	if n != 3 {
		t.Fatalf("12MB 应分 3 片，得 %d", n)
	}
	list := strings.Join(parts, ",")
	if !strings.HasPrefix(list, "1:") || strings.Count(list, ":") != 3 || strings.Count(list, ",") != 2 {
		t.Fatalf("part list 格式不对: %s", list)
	}
	// 每片 crc 是 8 位十六进制
	for _, p := range parts {
		kv := strings.Split(p, ":")
		if len(kv[1]) != 8 {
			t.Fatalf("crc 应为 8 位十六进制: %s", p)
		}
	}
}
