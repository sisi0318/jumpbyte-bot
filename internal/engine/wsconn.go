package engine

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/proxy"
)

// WsProxy 出口代理（socks5 默认，或 http/https）。
type WsProxy struct {
	Host, User, Pass string
	Port             int
	Scheme           string // socks5 / http / https
}

const maxFrameBytes = 33_554_432

// WsConn 对应 TS WsConnection：连接（可走代理）+ receive(timeout) 拉模型收包 + send/ping/close。
type WsConn struct {
	ws       *websocket.Conn
	msgs     chan []byte
	done     chan struct{}
	writeMu  sync.Mutex
	closeMu  sync.Mutex
	closed   atomic.Bool
	closeErr atomic.Value // error
}

// wsConnect 建立连接；握手失败抛错。headers 里的 Sec-WebSocket-Protocol 会转成子协议。
func wsConnect(rawURL string, headers map[string]string, px *WsProxy, handshakeTimeout time.Duration) (*WsConn, error) {
	if handshakeTimeout <= 0 {
		handshakeTimeout = 30 * time.Second
	}

	h := http.Header{}
	var protocols []string
	for k, v := range headers {
		lower := strings.ToLower(k)
		switch lower {
		case "host", "upgrade", "connection", "sec-websocket-key", "sec-websocket-version":
			// 由 gorilla 自己生成
		case "sec-websocket-protocol":
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					protocols = append(protocols, p)
				}
			}
		default:
			h.Set(k, v)
		}
	}

	d := websocket.Dialer{
		Subprotocols:      protocols,
		HandshakeTimeout:  handshakeTimeout,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		EnableCompression: false,
		ReadBufferSize:    32 * 1024,
		WriteBufferSize:   32 * 1024,
	}
	if err := applyProxy(&d, px); err != nil {
		return nil, err
	}

	ws, resp, err := d.Dial(rawURL, h)
	if err != nil {
		if resp != nil {
			return nil, errors.New("WebSocket handshake failed: HTTP " + resp.Status)
		}
		return nil, err
	}
	ws.SetReadLimit(maxFrameBytes)

	c := &WsConn{ws: ws, msgs: make(chan []byte, 256), done: make(chan struct{})}
	go c.reader()
	return c, nil
}

func applyProxy(d *websocket.Dialer, px *WsProxy) error {
	if px == nil || px.Host == "" {
		// 无显式代理时跟随 HTTP(S)_PROXY / ALL_PROXY / NO_PROXY 环境变量，
		// 让 WS 和 HTTP 一样能经终端代理抓包分析（TLS 已 InsecureSkipVerify，MITM 直通）。
		d.Proxy = http.ProxyFromEnvironment
		return nil
	}
	scheme := px.Scheme
	if scheme == "" {
		scheme = "socks5"
	}
	addr := net.JoinHostPort(px.Host, itoa(px.Port))

	if scheme == "http" || scheme == "https" {
		u := &url.URL{Scheme: scheme, Host: addr}
		if px.User != "" {
			u.User = url.UserPassword(px.User, px.Pass)
		}
		d.Proxy = http.ProxyURL(u)
		return nil
	}

	var auth *proxy.Auth
	if px.User != "" {
		auth = &proxy.Auth{User: px.User, Password: px.Pass}
	}
	sd, err := proxy.SOCKS5("tcp", addr, auth, proxy.Direct)
	if err != nil {
		return err
	}
	if cd, ok := sd.(proxy.ContextDialer); ok {
		d.NetDialContext = cd.DialContext
	} else {
		d.NetDial = sd.Dial
	}
	return nil
}

func (c *WsConn) reader() {
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			c.setClosed(err)
			return
		}
		select {
		case c.msgs <- data:
		case <-c.done:
			return
		}
	}
}

func (c *WsConn) setClosed(err error) {
	if c.closed.CompareAndSwap(false, true) {
		if err == nil {
			err = errors.New("WebSocket connection closed by server")
		}
		c.closeErr.Store(err)
		c.closeMu.Lock()
		close(c.done)
		c.closeMu.Unlock()
	}
}

func (c *WsConn) err() error {
	if v := c.closeErr.Load(); v != nil {
		return v.(error)
	}
	return errors.New("WebSocket socket already closed")
}

// IsOpen 连接是否可用。
func (c *WsConn) IsOpen() bool { return !c.closed.Load() }

// Receive 取一条消息；timeout 到返回 (nil,nil)；连接断开返回错误。
func (c *WsConn) Receive(timeout time.Duration) ([]byte, error) {
	select {
	case m := <-c.msgs:
		return m, nil
	default:
	}
	if c.closed.Load() {
		// 断开前也许还有已入队的消息
		select {
		case m := <-c.msgs:
			return m, nil
		default:
		}
		return nil, c.err()
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case m := <-c.msgs:
		return m, nil
	case <-t.C:
		return nil, nil
	case <-c.done:
		select {
		case m := <-c.msgs:
			return m, nil
		default:
		}
		return nil, c.err()
	}
}

// Send 发送二进制帧。
func (c *WsConn) Send(data []byte) error {
	if c.closed.Load() {
		return c.err()
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteMessage(websocket.BinaryMessage, data)
}

// Ping 发送 WebSocket ping 控制帧（失败静默，下次 Receive 会感知断开）。
func (c *WsConn) Ping() {
	if c.closed.Load() {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
}

// Close 关闭连接。
func (c *WsConn) Close() {
	c.setClosed(errors.New("closed by client"))
	c.writeMu.Lock()
	_ = c.ws.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	c.writeMu.Unlock()
	_ = c.ws.Close()
}
