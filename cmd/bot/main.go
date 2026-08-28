// gobot：私信 bot（Go 版，单静态二进制）。
//
//	gobot login      单独扫码登录 → 写 cookie.json
//	gobot --selftest 自检算法（a_bogus/sign/image/sqlite）
//	gobot            默认 cli：探测 cookie(失效自动扫码) → 连接 IM → 打印收到的消息(含图片链接)，可发消息
package main

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gobot/internal/abogus"
	"gobot/internal/config"
	"gobot/internal/engine"
	"gobot/internal/gateway"
	"gobot/internal/login"
	"gobot/internal/media"
	"gobot/internal/qr"
	"gobot/internal/sign"
	"gobot/internal/store"
	"gobot/internal/webapi"
)

func main() {
	args := os.Args[1:]
	if contains(args, "--selftest") {
		os.Exit(runSelfTest())
	}
	if len(args) > 0 && args[0] == "login" {
		runLogin()
		return
	}
	if len(args) > 0 && args[0] == "--smoke" {
		os.Exit(runSmoke())
	}
	runCli()
}

// runSmoke 向自己发一条带 nonce 的消息，若在收包连接上看到它回显，即证明发帧被服务端接收 + 收包解析可用。
func runSmoke() int {
	acc, err := config.LoadAccount()
	if err != nil {
		fmt.Println("[smoke] 无 cookie.json：" + err.Error())
		return 1
	}
	eng := engine.New(acc.Cookie, acc.UID, acc.DeviceID)
	nonce := "SMK" + sign.MsToken(6)
	selfConv := "0:1:" + acc.UID + ":" + acc.UID
	var hits int
	eng.OnRaw = func(b []byte) {
		if strings.Contains(string(b), nonce) {
			hits++
			fmt.Printf("[smoke] ✔ 收到含 nonce 的帧 len=%d\n", len(b))
		}
	}
	conn, err := eng.Connect()
	if err != nil {
		fmt.Println("[smoke] 连接失败：" + err.Error())
		return 1
	}
	fmt.Println("[smoke] 已连接，2s 后向自己发送 nonce=" + nonce)
	go func() {
		time.Sleep(2 * time.Second)
		if err := eng.SendText(selfConv, "gobot smoke "+nonce); err != nil {
			fmt.Println("[smoke] 发送失败：" + err.Error())
		} else {
			fmt.Println("[smoke] 已发送 → " + selfConv)
		}
	}()
	start := time.Now()
	eng.RunSession(conn, func(m engine.IncomingMessage) {
		fmt.Printf("[smoke] onMessage sender=%s text=%s\n", m.SenderID, m.Text)
	}, func() bool { return time.Since(start) > 12*time.Second })
	fmt.Printf("[smoke] 结束：nonce 命中帧数=%d\n", hits)
	if hits > 0 {
		return 0
	}
	return 2
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

func terminalHooks() login.Hooks {
	reader := bufio.NewReader(os.Stdin)
	return login.Hooks{
		OnQrcode: func(content, png, token string) {
			// 首选服务端权威 URL 直接编码；没有才退回解 PNG（goqr 会串位，仅兜底）。
			var q string
			var err error
			if content != "" {
				q, err = qr.RenderTerminal(content)
			} else {
				q, err = qr.PngToTerminalQR(png)
			}
			if err == nil {
				fmt.Println("\n请用 App 扫码登录：")
				fmt.Println(q)
			} else {
				fmt.Println("二维码渲染失败：" + err.Error())
			}
			if path, e := saveQRPng(content, png); e == nil {
				fmt.Println("二维码已存本地: " + path + "（扫图也行）")
			}
		},
		OnScanned: func(name string) { fmt.Println("已扫码：" + name + "，请在手机上点确认") },
		OnStatus:  func(m string) { fmt.Println("· " + m) },
		PromptSmsCode: func(mobile string) string {
			fmt.Print("短信验证码已发往 " + mobile + "，请输入：")
			s, _ := reader.ReadString('\n')
			return strings.TrimSpace(s)
		},
	}
}

// saveQRPng 把二维码存一份本地 PNG（优先用权威 URL 自己编码，否则退回服务端 PNG）。
func saveQRPng(content, pngBase64 string) (string, error) {
	var data []byte
	var err error
	switch {
	case content != "":
		data, err = qr.RenderPNG(content)
	case pngBase64 != "":
		data, err = base64.StdEncoding.DecodeString(pngBase64)
	default:
		return "", errors.New("无二维码内容")
	}
	if err != nil {
		return "", err
	}
	path := filepath.Join(config.Dir, "qrcode.png")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	if abs, e := filepath.Abs(path); e == nil {
		return abs, nil
	}
	return path, nil
}

func loginAndSave(deviceID string) (*config.Account, error) {
	r, err := login.QRLogin(terminalHooks(), deviceID)
	if err != nil {
		return nil, err
	}
	name := r.Name
	if name == "" {
		name = "账号" + r.UID
	}
	acc := &config.Account{ID: "main", Name: name, Cookie: r.Cookie, UID: r.UID, DeviceID: r.DeviceID, Channel: 1, Enabled: true}
	if err := config.SaveAccount(acc); err != nil {
		return nil, err
	}
	return acc, nil
}

func runLogin() {
	fmt.Println("=== 扫码登录（单账号）===")
	did := ""
	if a, err := config.LoadAccount(); err == nil {
		did = a.DeviceID
	}
	acc, err := loginAndSave(did)
	if err != nil {
		fmt.Fprintln(os.Stderr, "✘ 登录失败："+err.Error())
		os.Exit(1)
	}
	fmt.Printf("✔ 登录成功：uid=%s 名称=%s\n✔ 已写入 %s\n", acc.UID, acc.Name, config.CookiePath())
}

func runCli() {
	acc, err := config.LoadAccount()
	if err != nil {
		fmt.Println("[bot] 未找到 cookie.json，先扫码登录...")
		acc, err = loginAndSave("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "[bot] 启动失败："+err.Error())
			os.Exit(1)
		}
	} else {
		p := login.ProbeCookie(acc.Cookie, acc.DeviceID)
		switch {
		case p.Alive:
			fmt.Printf("[bot] cookie 有效：uid=%s %s\n", p.UID, p.Name)
		case p.Expired:
			fmt.Println("[bot] cookie 已失效（" + p.Reason + "），唤起扫码登录...")
			if acc, err = loginAndSave(acc.DeviceID); err != nil {
				fmt.Fprintln(os.Stderr, "[bot] 登录失败："+err.Error())
				os.Exit(1)
			}
		default:
			fmt.Println("[bot] cookie 探测异常（" + p.Reason + "），仍尝试启动")
		}
	}
	eng := engine.New(acc.Cookie, acc.UID, acc.DeviceID)

	// 网关：HTTP 发消息 + WS 收事件（像 QQ bot）。token 在 bot.json，首次自动生成。
	var gw *gateway.Gateway
	if bcfg, e := config.LoadBotConfig(); e == nil {
		gw = gateway.New(bcfg, acc, eng)
		if e := gw.Start(); e != nil {
			fmt.Println("[gateway] 启动失败：" + e.Error())
			gw = nil
		} else {
			eng.OnRaw = gw.EmitRaw
			fmt.Printf("[gateway] 已启动，令牌见 %s\n", config.BotConfigPath())
			fmt.Printf("[gateway]   收事件  ws://%s/ws?access_token=%s\n", gw.Addr(), bcfg.Token)
			fmt.Printf("[gateway]   原始帧  ws://%s/oriws?access_token=%s\n", gw.Addr(), bcfg.Token)
			fmt.Printf("[gateway]   发消息  POST http://%s/api/send_text  (Authorization: Bearer <令牌>)\n", gw.Addr())
			fmt.Printf("[gateway]   图片    GET  http://%s/img?u=<加密url>&k=<skey>\n", gw.Addr())
			fmt.Printf("[gateway]   存活    GET  http://%s/health\n", gw.Addr())
		}
	}

	fmt.Printf("[cli] 账号 %s (uid=%s) 就绪，连接 IM…\n", acc.Name, acc.UID)
	fmt.Println("[cli] 也可终端直接发：@<conv_id> <文本> 回车；Ctrl-C 退出。")

	// 打印 worker：昵称解析（可能阻塞网络）+ 打印，独立 goroutine，绝不挡收包。
	printCh := make(chan engine.IncomingMessage, 256)
	go func() {
		for m := range printCh {
			onIncoming(acc, m)
		}
	}()
	deliver := func(m engine.IncomingMessage) {
		if gw != nil {
			gw.EmitMessage(m) // 非阻塞（网关内部每连接有队列）
		}
		if m.Direction != "recv" {
			return // 自己发的不打印，避免回声
		}
		select {
		case printCh <- m:
		default: // 打印积压就丢，别挡收包
		}
	}

	// 优雅退出：Ctrl-C / SIGTERM → 关网关（断开 WS）后退出
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\n[bot] 收到退出信号，关闭…")
		if gw != nil {
			gw.Stop()
		}
		os.Exit(0)
	}()

	go stdinSender(eng)
	runEngineLoop(eng, acc, gw, deliver)
}

// onIncoming 打印一条收到的消息（图片透出本地代理链接，昵称走缓存解析）。
func onIncoming(acc *config.Account, m engine.IncomingMessage) {
	who := m.SenderID
	if m.SenderMs4 != "" {
		if u, ok := webapi.ResolveUsers(acc.Cookie, []string{m.SenderMs4}, acc.DeviceID)[m.SenderMs4]; ok && u.Nickname != "" {
			who = u.Nickname + "(" + m.SenderID + ")"
		}
	}
	ts := time.Now().Format("15:04:05")
	fmt.Printf("[%s] %s | conv=%s | %s\n", ts, who, m.ConvID, m.Text)
	if m.Image != nil {
		if raw := m.Image.PickURL(); raw != "" {
			fmt.Println("        └─ 图片: " + media.ImageLink(raw, m.Image.Skey))
		}
	}
}

// runEngineLoop 连接 → 收包 → 断线自动重连（带指数退避 + cookie 失效自愈）。
func runEngineLoop(eng *engine.Client, acc *config.Account, gw *gateway.Gateway, deliver func(engine.IncomingMessage)) {
	const maxBackoff = 60 * time.Second
	backoff := 2 * time.Second
	setState := func(s, m string) {
		if gw != nil {
			gw.SetState(s, m)
		}
	}
	emit := func(fn func()) {
		if gw != nil {
			fn()
		}
	}
	for {
		// 每轮先确认 cookie 还有效，失效就重新登录（否则会无限空转重连）
		if p := login.ProbeCookie(acc.Cookie, acc.DeviceID); p.Expired {
			fmt.Println("[engine] cookie 失效，重新登录…")
			setState("invalid", "cookie 失效")
			emit(func() { gw.EmitDisconnect("INVALID") })
			if !relogin(eng, acc) {
				time.Sleep(backoff)
				backoff = capDur(backoff*2, maxBackoff)
				continue
			}
			backoff = 2 * time.Second
		}

		setState("connecting", "connecting")
		conn, err := eng.Connect()
		if err != nil {
			fmt.Printf("[engine] 连接失败：%s，%v 后重试\n", err.Error(), backoff)
			setState("offline", "NETWORK")
			emit(func() { gw.EmitDisconnect("NETWORK") })
			time.Sleep(backoff)
			backoff = capDur(backoff*2, maxBackoff)
			continue
		}
		backoff = 2 * time.Second // 连上就重置退避
		fmt.Println("[engine] 已连接，开始收消息")
		emit(func() { gw.SetOnline(); gw.EmitConnect("online") })

		reason := eng.RunSession(conn, deliver, nil)
		fmt.Printf("[engine] 连接断开(%s)，稍后重连\n", reason)
		setState("offline", reason)
		emit(func() { gw.EmitDisconnect(reason) })
		time.Sleep(2 * time.Second)
	}
}

// relogin cookie 失效时重新扫码登录，原地更新 acc + eng（网关持有 acc 指针，同步生效）。
func relogin(eng *engine.Client, acc *config.Account) bool {
	na, err := loginAndSave(acc.DeviceID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[engine] 重新登录失败："+err.Error())
		return false
	}
	*acc = *na
	eng.Cookie, eng.CkUid, eng.DeviceID = na.Cookie, na.UID, na.DeviceID
	return true
}

func capDur(d, max time.Duration) time.Duration {
	if d > max {
		return max
	}
	return d
}

// stdinSender 从标准输入读  @<conv_id> <文本>  并发送。
func stdinSender(eng *engine.Client) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "@") {
			continue
		}
		rest := strings.TrimSpace(line[1:])
		i := strings.IndexAny(rest, " \t")
		if i <= 0 {
			fmt.Println("[send] 格式：@<conv_id> <文本>")
			continue
		}
		convID := rest[:i]
		text := strings.TrimSpace(rest[i+1:])
		if text == "" {
			continue
		}
		if err := eng.SendText(convID, text); err != nil {
			fmt.Println("[send] 失败：" + err.Error())
		} else {
			fmt.Println("[send] 已发送 → " + convID)
		}
	}
}

func runSelfTest() int {
	ok := true
	check := func(name string, pass bool) {
		mark := "[OK]  "
		if !pass {
			mark = "[FAIL]"
			ok = false
		}
		fmt.Println(" " + mark + " " + name)
	}

	ab := abogus.GetABogus("aid=339757&device_platform=PC", "", "Mozilla/5.0", time.Now().UnixMilli())
	check("a_bogus 生成", len(ab) > 50)

	s, qs := sign.SignParams(map[string]string{"aid": "339757", "device_platform": "PC"}, nil)
	check("sign (sha256)", len(s) == 64)
	check("qs (xor5)", len(qs) > 0)

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	iv := make([]byte, 12)
	_, _ = rand.Read(iv)
	plain := []byte("hello-douyin-image")
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCMWithNonceSize(block, 12)
	ct := gcm.Seal(nil, iv, plain, nil)
	dec, derr := media.DecryptImage(append(append([]byte{}, iv...), ct...), hex.EncodeToString(key))
	check("图片 AES-256-GCM 解密", derr == nil && string(dec) == string(plain))

	store.PutUsers([]store.CachedUser{{SecUID: "__selftest", UID: "1", Nickname: "n"}})
	check("sqlite 昵称缓存", store.GetCachedUsers([]string{"__selftest"})["__selftest"].Nickname == "n")

	if ok {
		fmt.Println("\n自检: 全部通过")
		return 0
	}
	fmt.Println("\n自检: 有失败")
	return 1
}
