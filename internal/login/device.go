package login

import (
	"crypto/rand"
	"strings"
)

// GenDeviceID 生成随机 device_id：324 开头 + 7 位随机数字（共 10 位）。
func GenDeviceID() string {
	var sb strings.Builder
	sb.WriteString("324")
	b := make([]byte, 7)
	_, _ = rand.Read(b)
	for _, x := range b {
		sb.WriteByte('0' + x%10)
	}
	return sb.String()
}
