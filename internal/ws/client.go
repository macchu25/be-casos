package ws

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	writeWait  = 60 * time.Second
	pongWait   = 120 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  2048,
	WriteBufferSize: 2048,
	CheckOrigin: func(r *http.Request) bool {
		return true // Cho phép mọi Origin trong môi trường Dev để ổn định kết nối
	},
}

// Client đại diện cho kết nối của 1 app (Frontend)
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	UserID string
}

func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		// Hệ thống backend Fall Detection chủ yếu Gửi 1 chiều (alert realtime), do đó readPump cơ bản chỉ dùng để handle chuẩn giao thức PingPong giữ Connect.
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Nối tất cả các message lấy ra từ channel
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			// Ping định kỳ để client biết vẫn sống
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Giúp nâng cấp Endpoint HTTP /ws thành kết nối hai chiều
func ServeWs(hub *Hub, c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		log.Println("[WebSocket] Từ chối kết nối: Thiếu UserID")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	log.Printf("[WebSocket] Yêu cầu kết nối mới từ User %s (%s)\n", userID, c.Request.RemoteAddr)
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("[WebSocket] Lỗi Upgrade:", err)
		return
	}
	client := &Client{hub: hub, conn: conn, send: make(chan []byte, 256), UserID: userID.(string)}
	
	// Register client vào Hub
	client.hub.Register <- client

	// Bắt tay mở 2 Goroutine chờ Gửi và Nhận message
	go client.writePump()
	go client.readPump()
}
