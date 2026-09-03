package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// BotConfig 网关配置（bot.json）。token 首次启动自动生成写回。
type BotConfig struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Token       string `json:"token"`        // 接入令牌，bot 用它连 WS 和调 HTTP
	QueueLimit  int    `json:"queue_limit"`  // 每连接事件积压上限
	EmitSelf    bool   `json:"emit_self"`    // 是否推 message_self（自己发的），单机自测用
	SendChannel string `json:"send_channel"` // 发送通道：ws=安卓 frontier WS；空/http=HTTP imapi(默认)
}

// BotConfigPath bot.json 路径。
func BotConfigPath() string { return filepath.Join(Dir, "bot.json") }

// LoadBotConfig 读 bot.json（缺省值兜底）；没有 token 就生成一个并写回。
func LoadBotConfig() (*BotConfig, error) {
	c := &BotConfig{Host: "127.0.0.1", Port: 9503, QueueLimit: 1000}
	if data, err := os.ReadFile(BotConfigPath()); err == nil {
		_ = json.Unmarshal(data, c) // 覆盖到缺省值上
	}
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port == 0 {
		c.Port = 9503
	}
	if c.QueueLimit == 0 {
		c.QueueLimit = 1000
	}
	if c.Token == "" {
		c.Token = randToken()
		_ = SaveBotConfig(c)
	}
	return c, nil
}

// SaveBotConfig 覆盖写 bot.json。
func SaveBotConfig(c *BotConfig) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(BotConfigPath(), append(data, '\n'), 0o644)
}

func randToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
