package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
)

// 发视频：封面走图片上传(UploadImage)拿 poster，视频走 TOS 分片上传(init→transfer→finish→commit)拿 video，
// 再选传一张审核图(check_pics, maya_review 空间)。content 逆自真机 HAR。
const videoPartSize = 5 * 1024 * 1024 // 5MB / 分片

type imapiVideoContent struct {
	Video struct {
		Tkey string `json:"tkey"`
		Md5  string `json:"md5"`
		Skey string `json:"skey"`
	} `json:"video"`
	Poster struct {
		Oid  string `json:"oid"`
		Md5  string `json:"md5"`
		Skey string `json:"skey"`
	} `json:"poster"`
	Height    int      `json:"height"`
	Width     int      `json:"width"`
	CheckPics []string `json:"check_pics"`
}

// SendVideoResult 发视频。cover 是封面图字节（必填，用作 poster + 审核图）。width/height 传 0 则用封面尺寸兜底。
func (c *Client) SendVideoResult(convID string, shortID uint64, videoBytes, coverBytes []byte, width, height int) (SendResult, error) {
	var res SendResult
	if len(videoBytes) == 0 {
		return res, fmt.Errorf("空视频")
	}
	if len(coverBytes) == 0 {
		return res, fmt.Errorf("需要封面图 cover")
	}
	if strings.TrimSpace(c.CkUid) == "" {
		return res, fmt.Errorf("未初始化：缺少 user_id")
	}
	creds, err := c.getUploadConfig()
	if err != nil {
		return res, fmt.Errorf("拿上传凭证失败: %w", err)
	}
	poster, err := c.UploadImage(coverBytes)
	if err != nil {
		return res, fmt.Errorf("封面上传失败: %w", err)
	}
	tkey, vskey, vmd5, err := c.uploadVideo(creds, videoBytes)
	if err != nil {
		return res, fmt.Errorf("视频上传失败: %w", err)
	}
	checkPic := c.uploadCheckPic(creds, coverBytes) // best-effort

	if width == 0 {
		width = poster.CoverWidth
	}
	if height == 0 {
		height = poster.CoverHeight
	}
	var content imapiVideoContent
	content.Video.Tkey, content.Video.Md5, content.Video.Skey = tkey, vmd5, vskey
	content.Poster.Oid, content.Poster.Md5, content.Poster.Skey = poster.Oid, poster.Md5, poster.Skey
	content.Height, content.Width = height, width
	content.CheckPics = []string{}
	if checkPic != "" {
		content.CheckPics = []string{checkPic}
	}
	return c.sendIMAPI(convID, shortID, jsonNoEscape(content))
}

// uploadVideo TOS 分片上传视频，返回 tkey/skey/md5。
func (c *Client) uploadVideo(cr stsCreds, data []byte) (tkey, skey, md5s string, err error) {
	size := len(data)
	storeURI, auth, host, sessionKey, err := c.applyUpload(cr, cr.SpaceName, "video", size)
	if err != nil {
		return "", "", "", err
	}
	uploadID, err := c.chunkInit(host, storeURI, auth)
	if err != nil {
		return "", "", "", err
	}
	var parts []string
	n := 0
	for off := 0; off < size; off += videoPartSize {
		end := off + videoPartSize
		if end > size {
			end = size
		}
		n++
		crc := fmt.Sprintf("%08x", crc32.ChecksumIEEE(data[off:end]))
		if err = c.chunkTransfer(host, storeURI, auth, uploadID, n, crc, data[off:end]); err != nil {
			return "", "", "", err
		}
		parts = append(parts, fmt.Sprintf("%d:%s", n, crc))
	}
	if err = c.chunkFinish(host, storeURI, auth, uploadID, strings.Join(parts, ",")); err != nil {
		return "", "", "", err
	}
	return c.commitUpload(cr, sessionKey) // Encryption.Uri/SecretKey/SourceMd5
}

// chunkInit phase=init，返回 uploadid。
func (c *Client) chunkInit(host, storeURI, auth string) (string, error) {
	boundary := "----WebKitFormBoundary" + randLower(16)
	body := []byte("--" + boundary + "--\r\n")
	req, _ := http.NewRequest("POST", "https://"+host+"/upload/v1/"+storeURI+"?phase=init", bytes.NewReader(body))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("X-Storage-U", c.CkUid)
	req.Header.Set("User-Agent", pcUA)
	resp, err := imHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var j struct {
		Code int `json:"code"`
		Data struct {
			UploadID string `json:"uploadid"`
		} `json:"data"`
	}
	rb, _ := io.ReadAll(resp.Body)
	if json.Unmarshal(rb, &j) != nil || j.Code != 2000 || j.Data.UploadID == "" {
		return "", fmt.Errorf("init 失败: %s", snippet(rb))
	}
	return j.Data.UploadID, nil
}

// chunkTransfer phase=transfer 上传一片。
func (c *Client) chunkTransfer(host, storeURI, auth, uploadID string, partNum int, crc string, part []byte) error {
	url := fmt.Sprintf("https://%s/upload/v1/%s?uploadid=%s&part_number=%d&phase=transfer", host, storeURI, uploadID, partNum)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(part))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-CRC32", crc)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Disposition", `attachment; filename="undefined"`)
	req.Header.Set("X-Storage-U", c.CkUid)
	req.Header.Set("User-Agent", pcUA)
	resp, err := uploadHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(rb, []byte(`"code":2000`)) {
		return fmt.Errorf("part %d 失败: %s", partNum, snippet(rb))
	}
	return nil
}

// chunkFinish phase=finish，body 为 "1:crc,2:crc,..."。
func (c *Client) chunkFinish(host, storeURI, auth, uploadID, partList string) error {
	url := fmt.Sprintf("https://%s/upload/v1/%s?phase=finish&uploadid=%s", host, storeURI, uploadID)
	req, _ := http.NewRequest("POST", url, strings.NewReader(partList))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("X-Storage-U", c.CkUid)
	req.Header.Set("User-Agent", pcUA)
	resp, err := imHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(rb, []byte(`"code":2000`)) {
		return fmt.Errorf("finish 失败: %s", snippet(rb))
	}
	return nil
}

// uploadCheckPic 把封面传到 maya_review 空间作审核图，返回 StoreUri（失败返回 ""，不阻断发送）。
func (c *Client) uploadCheckPic(cr stsCreds, cover []byte) string {
	storeURI, auth, host, _, err := c.applyUpload(cr, "maya_review", "image", len(cover))
	if err != nil {
		return ""
	}
	crc := fmt.Sprintf("%08x", crc32.ChecksumIEEE(cover))
	if c.tosPut(host, storeURI, auth, crc, cover) != nil {
		return ""
	}
	return storeURI
}
