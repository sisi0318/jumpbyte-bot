// Package abogus 1:1 转译自 NewabComplete_1.0.1.20.js（web a_bogus）。
// 全程用 []int 承载“字符码数组”（对应 JS 的 String.fromCharCode/charCodeAt），避免编码走样。
package abogus

// ctRotl 循环左移 32 位（JS: (e<<t | e>>>32-t) >>> 0）。
func ctRotl(e uint32, t uint) uint32 {
	t %= 32
	if t == 0 {
		return e
	}
	return (e << t) | (e >> (32 - t))
}

func stConst(e int) uint32 {
	if e >= 0 && e < 16 {
		return 2043430169
	}
	return 2055708042
}

func ktFF(e int, t, r, n uint32) uint32 {
	if e < 16 {
		return t ^ r ^ n
	}
	return (t & r) | (t & n) | (r & n)
}

func xtGG(e int, t, r, n uint32) uint32 {
	if e < 16 {
		return t ^ r ^ n
	}
	return (t & r) | (^t & n)
}

// sm3reg 对应 JS 里的 reg 对象。
type sm3reg struct {
	chunk []int
	reg   [8]uint32
	size  int
}

func newReg() *sm3reg {
	return &sm3reg{
		reg: [8]uint32{1937774191, 1226093241, 388252375, 3666478592, 2842636476, 372324522, 3817729613, 2969243214},
	}
}

func (g *sm3reg) compress(r []int) {
	if len(r) < 64 {
		return
	}
	t := make([]uint32, 132)
	for i := 0; i < 16; i++ {
		t[i] = uint32(r[4*i])<<24 | uint32(r[4*i+1])<<16 | uint32(r[4*i+2])<<8 | uint32(r[4*i+3])
	}
	for n := 16; n < 68; n++ {
		o := t[n-16] ^ t[n-9] ^ ctRotl(t[n-3], 15)
		o = o ^ ctRotl(o, 15) ^ ctRotl(o, 23)
		t[n] = o ^ ctRotl(t[n-13], 7) ^ t[n-6]
	}
	for n := 0; n < 64; n++ {
		t[n+68] = t[n] ^ t[n+4]
	}
	i := g.reg // copy
	for a := 0; a < 64; a++ {
		c := ctRotl(i[0], 12) + i[4] + ctRotl(stConst(a), uint(a))
		c = ctRotl(c, 7)
		f := c ^ ctRotl(i[0], 12)
		u := ktFF(a, i[0], i[1], i[2])
		u = u + i[3] + f + t[a+68]
		s := xtGG(a, i[4], i[5], i[6])
		s = s + i[7] + c + t[a]
		i[3] = i[2]
		i[2] = ctRotl(i[1], 9)
		i[1] = i[0]
		i[0] = u
		i[7] = i[6]
		i[6] = ctRotl(i[5], 19)
		i[5] = i[4]
		i[4] = s ^ ctRotl(s, 9) ^ ctRotl(s, 17)
	}
	for l := 0; l < 8; l++ {
		g.reg[l] = g.reg[l] ^ i[l]
	}
}

func (g *sm3reg) write(o []int) {
	g.size += len(o)
	i := 64 - len(g.chunk)
	if len(o) < i {
		g.chunk = append(g.chunk, o...)
		return
	}
	end := i
	if end > len(o) {
		end = len(o)
	}
	g.chunk = append(g.chunk, o[:end]...)
	for len(g.chunk) >= 64 {
		g.compress(g.chunk[:64])
		if i < len(o) {
			hi := i + 64
			if hi > len(o) {
				hi = len(o)
			}
			g.chunk = append([]int(nil), o[i:hi]...)
		} else {
			g.chunk = nil
		}
		i += 64
	}
}

func (g *sm3reg) fill() {
	o := 8 * g.size
	g.chunk = append(g.chunk, 128)
	i := len(g.chunk) % 64
	if 64-i < 8 {
		i -= 64
	}
	for ; i < 56; i++ {
		g.chunk = append(g.chunk, 0)
	}
	for a := 0; a < 4; a++ {
		c := o / 4294967296
		g.chunk = append(g.chunk, (c>>(8*(3-a)))&255)
	}
	for a := 0; a < 4; a++ {
		g.chunk = append(g.chunk, (o>>(8*(3-a)))&255)
	}
}

// getArr 自定义 SM3：输入字符码数组，返回 32 字节（字符码数组）。
func getArr(input []int) []int {
	g := newReg()
	g.write(input)
	g.fill()
	for i := 0; i+64 <= len(g.chunk); i += 64 {
		g.compress(g.chunk[i : i+64])
	}
	out := make([]int, 32)
	for i := 0; i < 8; i++ {
		c := g.reg[i]
		out[4*i+3] = int(c & 255)
		c >>= 8
		out[4*i+2] = int(c & 255)
		c >>= 8
		out[4*i+1] = int(c & 255)
		c >>= 8
		out[4*i] = int(c & 255)
	}
	return out
}

// codesOf 字符串 → 字符码数组（UTF-8 字节，对应 JS write 的 encodeURIComponent 口径）。
func codesOf(s string) []int {
	b := []byte(s)
	out := make([]int, len(b))
	for i, c := range b {
		out[i] = int(c)
	}
	return out
}

func getArrStr(s string) []int { return getArr(codesOf(s)) }
