package ws

type PrivateMessage struct {
	UserID string
	Data   []byte
}

// Hub duy trì danh sách các client đang kết nối và xử lý logic phát tin nhắn 
// Broadcast đến các client thuộc về User cụ thể.
type Hub struct {
	// Quản lý các client theo UserID để tối ưu broadcast (O(1) lookup user)
	userClients map[string]map[*Client]bool

	// Channel nhận dữ liệu cần push xuống cho các Client cụ thể
	Broadcast chan PrivateMessage

	// Đăng ký client mới
	Register chan *Client

	// Hủy đăng ký client khi bị ngắt kết nối
	Unregister chan *Client
}

func NewHub() *Hub {
	return &Hub{
		Broadcast:   make(chan PrivateMessage),
		Register:    make(chan *Client),
		Unregister:  make(chan *Client),
		userClients: make(map[string]map[*Client]bool),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			if h.userClients[client.UserID] == nil {
				h.userClients[client.UserID] = make(map[*Client]bool)
			}
			h.userClients[client.UserID][client] = true

		case client := <-h.Unregister:
			if clients, ok := h.userClients[client.UserID]; ok {
				if _, exists := clients[client]; exists {
					delete(clients, client)
					close(client.send)
					if len(clients) == 0 {
						delete(h.userClients, client.UserID)
					}
				}
			}

		case pm := <-h.Broadcast:
			if clients, ok := h.userClients[pm.UserID]; ok {
				for client := range clients {
					select {
					case client.send <- pm.Data:
					default:
						close(client.send)
						delete(clients, client)
						if len(clients) == 0 {
							delete(h.userClients, pm.UserID)
						}
					}
				}
			}
		}
	}
}
