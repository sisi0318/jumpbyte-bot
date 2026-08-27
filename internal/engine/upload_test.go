package engine

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 用真机 HAR 抓到的 STS 凭证 + 时间戳复算 ApplyUploadInner 的 SigV4 签名，
// 必须与服务端当时接受的签名逐字节一致——证明我们的 SigV4 实现正确。
func TestVodSigV4MatchesHAR(t *testing.T) {
	raw, err := os.ReadFile("testdata/vod_apply_ref.json")
	if err != nil {
		t.Skip("无 vod_apply_ref.json")
	}
	var ref struct {
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
		SessionToken    string `json:"session_token"`
		SpaceName       string `json:"space_name"`
		AmzDate         string `json:"amz_date"`
		ExpectedSig     string `json:"expected_sig"`
		FileSize        string `json:"file_size"`
		S               string `json:"s"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}

	cr := stsCreds{ref.AccessKeyID, ref.SecretAccessKey, ref.SessionToken, ref.SpaceName}
	query := map[string]string{
		"Action": "ApplyUploadInner", "Version": "2020-11-19", "SpaceName": ref.SpaceName,
		"FileType": "image", "IsInner": "1", "NeedFallback": "true",
		"FileSize": ref.FileSize, "s": ref.S,
	}
	cq := canonicalQuery(query)
	dateStamp := ref.AmzDate[:8] // 20260827
	auth, signed := vodSign("GET", cq, nil, cr, ref.AmzDate, dateStamp)

	// 取出 Signature=...
	i := strings.Index(auth, "Signature=")
	if i < 0 {
		t.Fatalf("auth 无 Signature: %s", auth)
	}
	got := auth[i+len("Signature="):]
	if got != ref.ExpectedSig {
		t.Fatalf("SigV4 签名不一致:\n 我们=%s\n HAR =%s\n canonicalQuery=%s", got, ref.ExpectedSig, cq)
	}
	// GET 不该带 x-amz-content-sha256
	if _, ok := signed["x-amz-content-sha256"]; ok {
		t.Fatal("GET 不应签 x-amz-content-sha256")
	}
	if !strings.Contains(auth, "SignedHeaders=x-amz-date;x-amz-security-token") {
		t.Fatalf("SignedHeaders 不对: %s", auth)
	}
}
