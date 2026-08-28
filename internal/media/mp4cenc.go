package media

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// CENC MP4 解密：定位每个 sample、AES-128-CTR 解密其 protected 段、再把加密盒子改名让文件"变明文"可播。
// 结构逆自真机 payload.mp4：非分片 MP4，senc/saiz/saio 在 stbl，tenc/frma 在 stsd/encv|enca/sinf。

type mp4box struct {
	typ       string
	start     int // 盒子起始（含 header）
	dataStart int // 内容起始（header 之后）
	size      int // 盒子总长
}

func u16(b []byte, p int) int { return int(binary.BigEndian.Uint16(b[p : p+2])) }
func u32(b []byte, p int) int { return int(binary.BigEndian.Uint32(b[p : p+4])) }
func u64(b []byte, p int) int { return int(binary.BigEndian.Uint64(b[p : p+8])) }

// boxesIn 解析 [start,end) 里的同层盒子。
func boxesIn(buf []byte, start, end int) []mp4box {
	var out []mp4box
	p := start
	for p+8 <= end {
		size := u32(buf, p)
		typ := string(buf[p+4 : p+8])
		hdr := 8
		if size == 1 {
			if p+16 > end {
				break
			}
			size = u64(buf, p+8)
			hdr = 16
		} else if size == 0 {
			size = end - p
		}
		if size < hdr || p+size > end {
			break
		}
		out = append(out, mp4box{typ, p, p + hdr, size})
		p += size
	}
	return out
}

func findBox(bs []mp4box, typ string) (mp4box, bool) {
	for _, b := range bs {
		if b.typ == typ {
			return b, true
		}
	}
	return mp4box{}, false
}

func (b mp4box) children(buf []byte) []mp4box { return boxesIn(buf, b.dataStart, b.start+b.size) }
func (b mp4box) end() int                     { return b.start + b.size }

// DecryptVideo 解密整段 CENC 视频，返回可播 MP4（原地改一份拷贝，长度不变）。
func DecryptVideo(mp4 []byte, skeyHex string) ([]byte, error) {
	key, err := hex.DecodeString(skeyHex)
	if err != nil || len(key) != 16 {
		return nil, fmt.Errorf("skey 应为 16 字节(32 hex): %q", skeyHex)
	}
	buf := make([]byte, len(mp4))
	copy(buf, mp4)

	top := boxesIn(buf, 0, len(buf))
	moov, ok := findBox(top, "moov")
	if !ok {
		return nil, fmt.Errorf("无 moov")
	}
	done := 0
	for _, trak := range trakList(buf, moov) {
		if err := decryptTrak(buf, trak, key); err != nil {
			if err == errNotEncrypted {
				continue
			}
			return nil, err
		}
		done++
	}
	if done == 0 {
		return nil, fmt.Errorf("没有可解密的加密轨（可能未加密或结构不符）")
	}
	return buf, nil
}

func trakList(buf []byte, moov mp4box) []mp4box {
	var out []mp4box
	for _, c := range moov.children(buf) {
		if c.typ == "trak" {
			out = append(out, c)
		}
	}
	return out
}

var errNotEncrypted = fmt.Errorf("未加密轨")

func decryptTrak(buf []byte, trak mp4box, key []byte) error {
	mdia, ok := findBox(trak.children(buf), "mdia")
	if !ok {
		return errNotEncrypted
	}
	minf, ok := findBox(mdia.children(buf), "minf")
	if !ok {
		return errNotEncrypted
	}
	stbl, ok := findBox(minf.children(buf), "stbl")
	if !ok {
		return errNotEncrypted
	}
	sc := stbl.children(buf)
	stsd, ok := findBox(sc, "stsd")
	if !ok {
		return errNotEncrypted
	}
	// stsd 内容前 8 字节是 version+flags+entry_count，之后是 sample entry
	entries := boxesIn(buf, stsd.dataStart+8, stsd.end())
	if len(entries) == 0 {
		return errNotEncrypted
	}
	entry := entries[0]
	var visualHdr int
	switch entry.typ {
	case "encv":
		visualHdr = 78
	case "enca":
		visualHdr = 28
	default:
		return errNotEncrypted // 不是加密 sample entry
	}
	sinf, ok := findBox(boxesIn(buf, entry.dataStart+visualHdr, entry.end()), "sinf")
	if !ok {
		return errNotEncrypted
	}
	frma, ok := findBox(sinf.children(buf), "frma")
	if !ok {
		return fmt.Errorf("无 frma")
	}
	dataFormat := string(buf[frma.dataStart : frma.dataStart+4])
	schi, ok := findBox(sinf.children(buf), "schi")
	if !ok {
		return fmt.Errorf("无 schi")
	}
	tenc, ok := findBox(schi.children(buf), "tenc")
	if !ok {
		return fmt.Errorf("无 tenc")
	}
	ivSize := int(buf[tenc.dataStart+7]) // default_Per_Sample_IV_Size

	senc, ok := findBox(sc, "senc")
	if !ok {
		return errNotEncrypted // 无 senc = 无逐样本 IV
	}
	stsz, ok := findBox(sc, "stsz")
	if !ok {
		return fmt.Errorf("无 stsz")
	}
	stsc, ok := findBox(sc, "stsc")
	if !ok {
		return fmt.Errorf("无 stsc")
	}
	chunkOffsets, err := parseChunkOffsets(buf, sc)
	if err != nil {
		return err
	}

	sizes := parseStsz(buf, stsz)
	offsets := computeSampleOffsets(sizes, parseStsc(buf, stsc), chunkOffsets)
	samples := parseSenc(buf, senc, ivSize)
	if len(samples) != len(sizes) {
		return fmt.Errorf("senc(%d) 与 stsz(%d) 样本数不一致", len(samples), len(sizes))
	}

	for i := range sizes {
		off, sz := offsets[i], sizes[i]
		if off <= 0 || off+sz > len(buf) {
			return fmt.Errorf("样本 %d 越界: off=%d sz=%d", i, off, sz)
		}
		dec, err := cencDecryptSample(key, samples[i].iv, buf[off:off+sz], samples[i].subs)
		if err != nil {
			return err
		}
		copy(buf[off:off+sz], dec)
	}

	// 拆封：改名 fourcc（长度不变，偏移不动）→ 文件"变明文"
	copy(buf[entry.start+4:entry.start+8], []byte(dataFormat)) // encv→hvc1 / enca→mp4a
	renameBox(buf, sinf, "free")
	renameBox(buf, senc, "free")
	if b, ok := findBox(sc, "saiz"); ok {
		renameBox(buf, b, "free")
	}
	if b, ok := findBox(sc, "saio"); ok {
		renameBox(buf, b, "free")
	}
	return nil
}

func renameBox(buf []byte, b mp4box, typ string) { copy(buf[b.start+4:b.start+8], []byte(typ)) }

func parseStsz(buf []byte, b mp4box) []int {
	p := b.dataStart
	sampleSize := u32(buf, p+4)
	count := u32(buf, p+8)
	sizes := make([]int, count)
	if sampleSize != 0 {
		for i := range sizes {
			sizes[i] = sampleSize
		}
		return sizes
	}
	q := p + 12
	for i := 0; i < count; i++ {
		sizes[i] = u32(buf, q)
		q += 4
	}
	return sizes
}

func parseChunkOffsets(buf []byte, sc []mp4box) ([]int, error) {
	if b, ok := findBox(sc, "stco"); ok {
		count := u32(buf, b.dataStart+4)
		out := make([]int, count)
		q := b.dataStart + 8
		for i := 0; i < count; i++ {
			out[i] = u32(buf, q)
			q += 4
		}
		return out, nil
	}
	if b, ok := findBox(sc, "co64"); ok {
		count := u32(buf, b.dataStart+4)
		out := make([]int, count)
		q := b.dataStart + 8
		for i := 0; i < count; i++ {
			out[i] = u64(buf, q)
			q += 8
		}
		return out, nil
	}
	return nil, fmt.Errorf("无 stco/co64")
}

type stscEntry struct{ firstChunk, samplesPerChunk int }

func parseStsc(buf []byte, b mp4box) []stscEntry {
	count := u32(buf, b.dataStart+4)
	out := make([]stscEntry, count)
	q := b.dataStart + 8
	for i := 0; i < count; i++ {
		out[i] = stscEntry{u32(buf, q), u32(buf, q+4)}
		q += 12
	}
	return out
}

// computeSampleOffsets 按 stsc/stco/stsz 算每个 sample 的绝对文件偏移。
func computeSampleOffsets(sizes []int, stsc []stscEntry, chunkOffsets []int) []int {
	offs := make([]int, len(sizes))
	si := 0
	for ci := 0; ci < len(chunkOffsets) && si < len(sizes); ci++ {
		spc := samplesInChunk(stsc, ci+1) // 1-indexed
		off := chunkOffsets[ci]
		for s := 0; s < spc && si < len(sizes); s++ {
			offs[si] = off
			off += sizes[si]
			si++
		}
	}
	return offs
}

func samplesInChunk(stsc []stscEntry, chunk1 int) int {
	spc := 0
	for _, e := range stsc {
		if e.firstChunk <= chunk1 {
			spc = e.samplesPerChunk
		} else {
			break
		}
	}
	return spc
}

type sencSample struct {
	iv   []byte
	subs []Subsample
}

func parseSenc(buf []byte, b mp4box, ivSize int) []sencSample {
	p := b.dataStart
	flags := u32(buf, p) & 0xffffff
	count := u32(buf, p+4)
	p += 8
	out := make([]sencSample, count)
	for i := 0; i < count; i++ {
		iv := make([]byte, ivSize)
		copy(iv, buf[p:p+ivSize])
		p += ivSize
		var subs []Subsample
		if flags&2 != 0 {
			n := u16(buf, p)
			p += 2
			subs = make([]Subsample, n)
			for j := 0; j < n; j++ {
				subs[j] = Subsample{Clear: u16(buf, p), Protected: u32(buf, p+2)}
				p += 6
			}
		}
		out[i] = sencSample{iv, subs}
	}
	return out
}
