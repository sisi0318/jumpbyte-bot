package sign

import "testing"

// 期望值取自已对真机 HAR 验证过的 TS 实现（同一输入）。
func TestXor5(t *testing.T) {
	if got := Xor5("438558"); got != "31363d30303d" {
		t.Fatalf("Xor5(438558)=%s want 31363d30303d", got)
	}
}

func TestSignParams(t *testing.T) {
	q := map[string]string{
		"aid": "339757", "device_platform": "PC", "account_sdk_source": "web",
		"biz_trace_id": "abcd1234", "device_id": "0", "iid": "0",
		"is_from_iesaccountsaas": "1", "is_from_ttaccountsdk": "1", "is_new_login": "1",
		"account_sdk_source_info": "blob",
	}
	body := map[string]string{"token": "tok_lf", "is_new_login": "1", "next": "https://www.douyin.com"}

	const wantQS = "6466666a706b715a76616e5a766a70776660296466666a706b715a76616e5a766a707766605a6c6b636a29646c6129676c7f5a71776466605a6c61296160736c66605a6c61296160736c66605a75696471636a7768296c6c61296c765a63776a685a6c60766466666a706b7176646476296c765a63776a685a71716466666a706b7176616e296c765a6b60725a696a626c6b"
	const wantGET = "895374c71a1edf65d7479f4316b59182a9f6f4baa34f144cd29923addd84fa74"
	const wantPOST = "040fea2ba3dfeef4b7f485ac806887c338c526fb1168664812b95e82649cca99"

	sg, qs := SignParams(q, nil)
	if qs != wantQS {
		t.Errorf("qs mismatch\n got %s\nwant %s", qs, wantQS)
	}
	if sg != wantGET {
		t.Errorf("sign(GET) got %s want %s", sg, wantGET)
	}
	sp, _ := SignParams(q, body)
	if sp != wantPOST {
		t.Errorf("sign(POST) got %s want %s", sp, wantPOST)
	}
}

func TestMsToken(t *testing.T) {
	if len(MsToken(128)) != 128 {
		t.Fatal("msToken length")
	}
}
