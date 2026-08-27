package abogus

import "testing"

// Park-Miller LCG，与对拍用的 JS 补丁一致。
func lcgRand() func() float64 {
	seed := int64(1)
	return func() float64 {
		seed = (seed * 48271) % 2147483647
		return float64(seed-1) / 2147483646.0
	}
}

func TestABogusVsJS(t *testing.T) {
	c := &ctx{now: 1787830000000, rand: lcgRand()}
	got := c.generate("aid=339757&device_platform=PC", "", "Mozilla/5.0 (Windows NT 10.0)")
	const want = "DX4Vhe7LmZQbKVFSYCBP9vKU-CjlNsuyCFi/WH/PyOzLLqeYFuNcQnc-jxLWslogK8MkwI171nz/bEncpsUspenkFmpDu0sj845VIzmL/Z7sbsJhJrg2CjSxFk4PW/GO8QASi27RIsBiIxo5nNCzAdlSq/-rBcbDQ1-GVITSO2ym-SWc27qdYKEXSk3cQTx1sjm="
	if got != want {
		t.Errorf("a_bogus mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestABogusProd(t *testing.T) {
	// 生产入口应能跑出 ~196 长度、无 panic
	out := GetABogus("aid=339757", "", "Mozilla/5.0", 1787830000000)
	if len(out) < 100 {
		t.Fatalf("a_bogus 长度异常: %d", len(out))
	}
}
