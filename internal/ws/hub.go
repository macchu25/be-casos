package ws

// Hub duy trì danh sách các client đang kết nối và xử lý logic phát tin nhắn 
// Broadcast đến tất cả các client đó (ví dụ truyền tín hiệu alert real-time).
type Hub struct {
	// Quản lý các client web socket đang connect
	clients map[*Client]bool

	// Channel nhận dữ liệu cần push xuống cho toàn bộ các Client
	Broadcast chan []byte

	// Đăng ký client mới
	Register chan *Client

	// Hủy đăng ký client khi bị ngắt kết nối
	Unregister chan *Client
}

func NewHub() *Hub {
	return &Hub{
		Broadcast:  make(chan []byte),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.clients[client] = true
		case client := <-h.Unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
		case message := <-h.Broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}
