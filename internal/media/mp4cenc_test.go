package media

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// 用真机加密样本 har/payload.mp4 跑完整 CENC 解密管线，再用 ffprobe/ffmpeg 验证输出可解码。
// payload.mp4 在 har/（gitignore），缺失则跳过；解密产物写 t.TempDir()，不入库。
func TestDecryptVideoPayload(t *testing.T) {
	const skey = "051d4c7b2d67fba130b4134745d15a6a"
	enc, err := os.ReadFile(filepath.Join("..", "..", "har", "payload.mp4"))
	if err != nil {
		t.Skip("无 har/payload.mp4")
	}

	out, err := DecryptVideo(enc, skey)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if len(out) != len(enc) {
		t.Fatalf("长度变了: %d → %d（CTR 应保持等长）", len(enc), len(out))
	}

	// 结构校验：拆封后加密盒子应消失，明文样本入口出现，mdat 原封不动
	if bytes.Contains(out, []byte("encv")) || bytes.Contains(out, []byte("enca")) {
		t.Error("仍存在 encv/enca 样本入口")
	}
	if bytes.Contains(out, []byte("senc")) || bytes.Contains(out, []byte("sinf")) {
		t.Error("仍存在 senc/sinf 加密盒子")
	}
	if !bytes.Contains(out, []byte("hvc1")) || !bytes.Contains(out, []byte("mp4a")) {
		t.Error("未生成 hvc1/mp4a 明文样本入口")
	}
	assertMdatIntact(t, enc, out)

	// 首个视频样本：解密后长度前缀应恰好切分（证明 stsc/stco/stsz 偏移算对了）
	assertFirstVideoSampleNAL(t, out)

	// ffprobe/ffmpeg 端到端：能识别 hevc+aac 且能真正解码前几帧 → 解密正确
	dst := filepath.Join(t.TempDir(), "clear.mp4")
	if err := os.WriteFile(dst, out, 0o644); err != nil {
		t.Fatal(err)
	}
	ffprobeCheck(t, dst)
	ffmpegDecodeCheck(t, dst)
}

// mdat 内容区不参与改名，且 CTR 等长；这里确认 mdat 盒子头位置与大小没动。
func assertMdatIntact(t *testing.T, enc, out []byte) {
	t.Helper()
	ei := bytes.Index(enc, []byte("mdat"))
	oi := bytes.Index(out, []byte("mdat"))
	if ei < 0 || ei != oi {
		t.Fatalf("mdat 位置变化: %d → %d", ei, oi)
	}
	// mdat 盒子头（size+type）应完全一致
	if !bytes.Equal(enc[ei-4:ei+4], out[oi-4:oi+4]) {
		t.Fatal("mdat 盒子头被改动")
	}
}

// 走一遍首视频样本的 4 字节长度前缀链，累加应正好等于样本大小。
func assertFirstVideoSampleNAL(t *testing.T, out []byte) {
	t.Helper()
	off, size := firstVideoSample(t, out)
	data := out[off : off+size]
	p := 0
	nals := 0
	for p+4 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[p : p+4]))
		p += 4 + n
		nals++
		if n <= 0 || p > len(data) {
			t.Fatalf("首样本 NAL 长度前缀不自洽: nal#%d len=%d 越界(p=%d,size=%d)", nals, n, p, len(data))
		}
	}
	if p != len(data) {
		t.Fatalf("首样本 NAL 未精确覆盖: 走到 %d ≠ %d", p, len(data))
	}
	t.Logf("首视频样本 off=%d size=%d nals=%d", off, size, nals)
}

// 从解密后的 MP4 里取视频轨第一个样本的绝对偏移与大小（复用生产解析器）。
func firstVideoSample(t *testing.T, buf []byte) (int, int) {
	t.Helper()
	moov, ok := findBox(boxesIn(buf, 0, len(buf)), "moov")
	if !ok {
		t.Fatal("无 moov")
	}
	for _, trak := range trakList(buf, moov) {
		mdia, _ := findBox(trak.children(buf), "mdia")
		minf, _ := findBox(mdia.children(buf), "minf")
		stbl, ok := findBox(minf.children(buf), "stbl")
		if !ok {
			continue
		}
		sc := stbl.children(buf)
		stsd, _ := findBox(sc, "stsd")
		entries := boxesIn(buf, stsd.dataStart+8, stsd.end())
		if len(entries) == 0 || entries[0].typ != "hvc1" {
			continue
		}
		stsz, _ := findBox(sc, "stsz")
		stsc, _ := findBox(sc, "stsc")
		chunks, err := parseChunkOffsets(buf, sc)
		if err != nil {
			t.Fatal(err)
		}
		sizes := parseStsz(buf, stsz)
		offs := computeSampleOffsets(sizes, parseStsc(buf, stsc), chunks)
		return offs[0], sizes[0]
	}
	t.Fatal("未找到 hvc1 视频轨")
	return 0, 0
}

func ffprobeCheck(t *testing.T, path string) {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Log("无 ffprobe，跳过端到端解码校验")
		return
	}
	outb, err := exec.Command(bin, "-v", "error", "-show_entries",
		"stream=codec_name", "-of", "default=nw=1:nk=1", path).CombinedOutput()
	got := string(outb)
	if err != nil {
		t.Fatalf("ffprobe 失败: %v\n%s", err, got)
	}
	if !bytes.Contains(outb, []byte("hevc")) || !bytes.Contains(outb, []byte("aac")) {
		t.Fatalf("ffprobe 未识别 hevc+aac:\n%s", got)
	}
	t.Logf("ffprobe codecs:\n%s", got)
}

func ffmpegDecodeCheck(t *testing.T, path string) {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Log("无 ffmpeg，跳过帧解码校验")
		return
	}
	// 真正解码前 10 帧视频 + 全部音频到 null；解密错则 HEVC/AAC 解码会报错
	outb, err := exec.Command(bin, "-v", "error", "-i", path,
		"-frames:v", "10", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg 解码失败: %v\n%s", err, string(outb))
	}
	if len(bytes.TrimSpace(outb)) != 0 {
		t.Fatalf("ffmpeg 解码有错误输出:\n%s", string(outb))
	}
	t.Log("ffmpeg 解码前 10 帧无错误 → 解密正确")
}
