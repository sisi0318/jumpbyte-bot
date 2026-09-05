package engine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func wsURLOf(httpURL string) string { return "ws" + strings.TrimPrefix(httpURL, "http") }

// 服务端只发控制帧（ping）、一条数据都不发时，连接必须仍被判为「活着」。
// gorilla 在 ReadMessage 内部就把控制帧消化掉了，不会交给上层——早先的保活只看数据帧，
// 于是安静的会话会被自己按超时掐断、反复重连。
func TestIdleForCountsControlFrames(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		// 得有人读，才能驱动本端对 pong 的处理
		go func() {
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		}()
		for i := 0; i < 30; i++ { // 只发 ping，不发任何数据帧
			if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}))
	defer srv.Close()

	conn, err := wsConnect(wsURLOf(srv.URL), nil, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	time.Sleep(900 * time.Millisecond)

	// 确认这段时间里真的没有数据帧
	if raw, err := conn.Receive(50 * time.Millisecond); err != nil || len(raw) != 0 {
		t.Fatalf("不该收到数据帧：raw=%d err=%v", len(raw), err)
	}
	if idle := conn.IdleFor(); idle > 500*time.Millisecond {
		t.Fatalf("服务端每 100ms 一个 ping，IdleFor 却有 %v——控制帧没被计入保活", idle)
	}
}

// 数据帧同样刷新保活；连接断开后 Err() 要能给出原因（否则日志里只能看到一个干巴巴的 disconnect）。
func TestIdleForAndErrOnData(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = ws.WriteMessage(websocket.BinaryMessage, []byte("hi"))
		time.Sleep(200 * time.Millisecond)
		ws.Close()
	}))
	defer srv.Close()

	conn, err := wsConnect(wsURLOf(srv.URL), nil, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if conn.Err() != nil {
		t.Fatalf("刚连上 Err() 应为 nil，实际 %v", conn.Err())
	}
	raw, err := conn.Receive(2 * time.Second)
	if err != nil || string(raw) != "hi" {
		t.Fatalf("收数据失败：%q %v", raw, err)
	}
	if idle := conn.IdleFor(); idle > 500*time.Millisecond {
		t.Fatalf("刚收到数据，IdleFor 却有 %v", idle)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := conn.Receive(100 * time.Millisecond); err != nil {
			if conn.Err() == nil {
				t.Fatal("已断开，Err() 不该是 nil")
			}
			return
		}
	}
	t.Fatal("服务端已关闭，Receive 却一直没报错")
}
