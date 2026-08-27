package abogus

import (
	"crypto/rand"
	"encoding/binary"
	"strconv"
	"strings"
)

// ctx 承载可注入的时间与随机（便于对拍 JS）。
type ctx struct {
	now  int64          // Date.now() 毫秒
	rand func() float64 // Math.random()
}

// ---- base64 变体 ----

func encryptionUa(ss []int) []int {
	const str = "ckdp1h4ZKsUB80/Mfvw36XIgR25+WQAlEi7NLboqYTOPuzmFjJnryx9HVGDaStCe"
	var out []int
	j := 0
	for i := 0; i < len(ss); i += 3 {
		if i+3 <= len(ss) {
			number := ((ss[i] & 255) << 16) | ((ss[i+1] & 255) << 8) | (ss[i+2] & 255)
			out = append(out, int(str[(number&16515072)>>18]), int(str[(number&258048)>>12]), int(str[(number&4032)>>6]), int(str[number&63]))
		}
		if i+3 > len(ss) {
			switch len(ss) - j {
			case 2:
				b := ss[j+1]<<8 | ss[j]<<16
				out = append(out, int(str[(b&16515072)>>18]), int(str[(b&258048)>>12]), int(str[(b&4032)>>6]), int('='))
			case 1:
				b := ss[j] << 16
				out = append(out, int(str[(b&16515072)>>18]), int(str[(b&258048)>>12]), int('='), int('='))
			}
		}
		j += 3
	}
	return out
}

// ---- RC4 变体 ----

func abArr256() []int {
	nums := make([]int, 256)
	for i := range nums {
		nums[i] = 255 - i
	}
	prev := 0
	const lm = 211
	for i := 0; i < 256; i++ {
		prev = (prev*nums[i] + prev + lm) % 256
		nums[i], nums[prev] = nums[prev], nums[i]
	}
	return nums
}

func uaArr256(uaSalt int) []int {
	nums := make([]int, 256)
	for i := range nums {
		nums[i] = 255 - i
	}
	prev := 0
	lm := []int{0, 1, uaSalt} // String.fromCharCode(0.0039,1,ua_salt) → [0,1,ua_salt]
	for i := 0; i < 256; i++ {
		prev = (prev*nums[i] + prev + lm[i%3]) % 256
		nums[i], nums[prev] = nums[prev], nums[i]
	}
	return nums
}

func garble(arr256, input []int) []int {
	n4 := 0
	ans := make([]int, 0, len(input))
	for i := 0; i < len(input); i++ {
		n2 := (i + 1) % 256
		n4 = (n4 + arr256[n2]) % 256
		old := arr256[n2]
		arr256[n2] = arr256[n4]
		arr256[n4] = old
		n7 := (arr256[n2] + old) % 256
		ans = append(ans, input[i]^arr256[n7])
	}
	return ans
}

func uaGarbledCharacters(ua []int, uaSalt int) []int { return garble(uaArr256(uaSalt), ua) }
func abGarbledCharacters(input []int) []int          { return garble(abArr256(), input) }

// ---- 固定数据 / 派生 ----

func getArr2() []int {
	// JS switch 两分支最终都是这串（第二次赋值覆盖）
	return codesOf("784|943|1707|1019|1707|1019|1707|1067|MacIntel")
}

func getLast3Num(dateTime1 int64) []int {
	s := strconv.Itoa(int((dateTime1+3)&255)) + ","
	return codesOf(s)
}

func b8(v int64, k uint) int { return int((v >> k) & 255) }

func getLastNum2(arr1, arr []int) int {
	idx := []int{}
	// arr1[0..7]
	x := arr1[0] ^ arr1[1] ^ arr1[2] ^ arr1[3] ^ arr1[4] ^ arr1[5] ^ arr1[6] ^ arr1[7]
	for _, i := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 25, 26, 27, 29, 30, 31, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 50, 51, 53, 54} {
		x ^= arr[i]
	}
	_ = idx
	return x
}

func (c *ctx) getNumList(arr0, arrAr []int) []int {
	var numList []int
	for i := 0; i < len(arrAr); i += 3 {
		if i+2 >= len(arrAr) {
			if i+1 >= len(arrAr) {
				numList = append(numList, arrAr[i])
			} else {
				numList = append(numList, arrAr[i], arrAr[i+1])
			}
		} else {
			random := int(c.rand()*1000) & 255
			numList = append(numList,
				(random&145)|(arrAr[i]&110),
				(random&66)|(arrAr[i+1]&189),
				(random&44)|(arrAr[i+2]&211),
				((arrAr[i]&145)|(arrAr[i+1]&66))|(arrAr[i+2]&44))
		}
	}
	out := append([]int(nil), arr0...)
	return append(out, numList...)
}

func (c *ctx) topHeader() []int {
	num1 := int(c.rand()*65535) & 255
	num2 := int(c.rand() * 40)
	return []int{
		(num1 & 170) | (3 & 85), (num1 & 85) | (3 & 170),
		(num2 & 170) | (82 & 85), (num2 & 85) | (82 & 170),
	}
}

func (c *ctx) randomGarbledList() []int {
	r1 := int(c.rand() * 65535)
	num1a := r1 & 255
	num2a := (r1 >> 8) & 255
	num1b := int(c.rand() * 240)
	num2b := (int(c.rand()*255) & 77) | 2 | 16 | 32 | 128
	return []int{
		(num1a & 170) | (1 & 85), (num1a & 85) | (1 & 170), (num2a & 170) | (0 & 85), (num2a & 85) | (0 & 170),
		(num1b & 170) | (1 & 85), (num1b & 85) | (1 & 170), (num2b & 170) | (0 & 85), (num2b & 85) | (0 & 170),
	}
}

func (c *ctx) getArr29(dt1, dt2 int64, params, data, userAgent string) []int {
	parArr := getArr(getArrStr(params))
	dataArr := getArr(getArrStr(data))
	uaSalt := 0
	browserArr := getArr(encryptionUa(uaGarbledCharacters(codesOf(userAgent), uaSalt)))
	num := getArr2()
	dateTime3 := int((c.now - 1721836800000) / 1209600000)
	arr0 := c.randomGarbledList()

	arr := make([]int, 55)
	arr[0] = 41
	arr[1] = dateTime3
	arr[2] = 5
	arr[3] = (int(dt1-dt2) + 3) & 255
	arr[4] = b8(dt1, 0)
	arr[5] = b8(dt1, 8)
	arr[6] = b8(dt1, 16)
	arr[7] = b8(dt1, 24)
	arr[8] = b8(dt1, 32)
	arr[9] = b8(dt1, 40)
	arr[10] = 1
	arr[11] = 0
	arr[12] = 1
	arr[13] = 0
	arr[14] = 1
	arr[15] = 0
	arr[16] = 0
	arr[17] = 0
	arr[18] = uaSalt & 255
	arr[19] = (uaSalt >> 8) & 255
	arr[20] = (uaSalt >> 16) & 255
	arr[21] = (uaSalt >> 24) & 255
	arr[22] = parArr[9]
	arr[23] = parArr[18]
	arr[24] = 3
	arr[25] = parArr[3]
	arr[26] = dataArr[10]
	arr[27] = dataArr[19]
	arr[28] = 4
	arr[29] = dataArr[4]
	arr[30] = browserArr[11]
	arr[31] = browserArr[21]
	arr[32] = 5
	arr[33] = browserArr[5]
	arr[34] = b8(dt2, 0)
	arr[35] = b8(dt2, 8)
	arr[36] = b8(dt2, 16)
	arr[37] = b8(dt2, 24)
	arr[38] = b8(dt2, 32)
	arr[39] = b8(dt2, 40)
	arr[40] = 3
	arr32 := 6241
	arr[41] = (arr32 >> 0) & 255
	arr[42] = (arr32 >> 8) & 255
	arr[43] = (arr32 >> 16) & 255
	arr[44] = (arr32 >> 24) & 255
	arr36 := 6383
	arr[45] = arr36 & 255
	arr[46] = (arr36 >> 8) & 255
	arr[47] = (arr36 >> 16) & 255
	arr[48] = (arr36 >> 24) & 255
	lastNumOne := getLast3Num(dt1)
	arr[49] = len(num)
	arr[50] = len(num) & 255
	arr[51] = (len(num) >> 8) & 255
	arr[52] = len(lastNumOne)
	arr[53] = len(lastNumOne) & 255
	arr[54] = (len(lastNumOne) >> 8) & 255
	lastNum := getLastNum2(arr0, arr)

	order := []int{9, 18, 30, 35, 47, 4, 44, 19, 10, 23, 12, 40, 25, 42, 3, 22, 38, 21, 5, 45, 1, 29, 6, 43, 33, 14, 36, 37, 2, 46, 15, 48, 31, 26, 16, 13, 8, 41, 27, 17, 39, 20, 11, 0, 34, 7, 50, 51, 53, 54}
	arr2 := make([]int, len(order))
	for i, o := range order {
		arr2[i] = arr[o]
	}
	newArr2 := append(append(append([]int(nil), arr2...), num...), lastNumOne...)
	newArr2 = append(newArr2, lastNum)
	return c.getNumList(arr0, newArr2)
}

func (c *ctx) getGarbledString(params, data, userAgent string) []int {
	t1 := c.now
	t2 := t1 - int64(c.rand()*10)
	arr29 := c.getArr29(t1, t2, params, data, userAgent)
	a := c.topHeader()
	b := abGarbledCharacters(arr29)
	return append(a, b...)
}

// generate 用给定 ctx 生成 a_bogus。
func (c *ctx) generate(url, data, userAgent string) string {
	params := url[strings.Index(url, "?")+1:] + "dhzx"
	data += "dhzx"
	garbled := c.getGarbledString(params, data, userAgent)
	const shortStr = "Dkdpgh2ZmsQB80/MfvV36XI1R45-WUAlEixNLwoqYTOPuzKFjJnry79HbGcaStCe"
	var sb strings.Builder
	j := 0
	for i := 0; i <= len(garbled); i += 3 {
		if i+3 <= len(garbled) {
			baseNum := garbled[i+2] | garbled[i+1]<<8 | garbled[i]<<16
			sb.WriteByte(shortStr[(baseNum&16515072)>>18])
			sb.WriteByte(shortStr[(baseNum&258048)>>12])
			sb.WriteByte(shortStr[(baseNum&4032)>>6])
			sb.WriteByte(shortStr[baseNum&63])
		}
		if i+3 > len(garbled) {
			switch len(garbled) - j {
			case 2:
				baseNum := garbled[j+1]<<8 | garbled[j]<<16
				sb.WriteByte(shortStr[(baseNum&16515072)>>18])
				sb.WriteByte(shortStr[(baseNum&258048)>>12])
				sb.WriteByte(shortStr[(baseNum&4032)>>6])
				sb.WriteByte('=')
			case 1:
				baseNum := garbled[j] << 16
				sb.WriteByte(shortStr[(baseNum&16515072)>>18])
				sb.WriteByte(shortStr[(baseNum&258048)>>12])
				sb.WriteByte('=')
				sb.WriteByte('=')
			}
		}
		j += 3
	}
	return sb.String()
}

// cryptoFloat 返回 [0,1) 的加密随机（生产用）。
func cryptoFloat() float64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return float64(binary.BigEndian.Uint64(b[:])>>11) / (1 << 53)
}

// GetABogus 生产入口：真实时间 + 加密随机。url=query串(不含前导?可含)，data=POST体，userAgent=UA。
func GetABogus(url, data, userAgent string, nowMs int64) string {
	c := &ctx{now: nowMs, rand: cryptoFloat}
	return c.generate(url, data, userAgent)
}
