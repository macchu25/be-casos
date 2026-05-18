package camera

import (
	"context"
	"log"
	"sync"
	"time"

	"go-backend/internal/model"
	"go-backend/internal/stream"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type Manager struct {
	db        *mongo.Database
	hlsServer *stream.HLSServer
	cameras   map[primitive.ObjectID]context.CancelFunc
	mutex     sync.RWMutex
}

func NewManager(db *mongo.Database, hlsServer *stream.HLSServer) *Manager {
	return &Manager{
		db:        db,
		hlsServer: hlsServer,
		cameras:   make(map[primitive.ObjectID]context.CancelFunc),
	}
}

// Khởi chạy toàn bộ camera hiện có trong CSDL
func (m *Manager) StartAll() {
	var cams []model.Camera
	cursor, err := m.db.Collection("cameras").Find(context.Background(), bson.M{})
	if err != nil {
		log.Printf("[Manager] Lỗi khi lấy danh sách camera: %v\n", err)
		return
	}
	if err := cursor.All(context.Background(), &cams); err != nil {
		log.Printf("[Manager] Lỗi khi decode danh sách camera: %v\n", err)
	}

	for _, cam := range cams {
		m.StartStream(cam)
	}
}

// Chạy Goroutine cho 1 camera
func (m *Manager) StartStream(cam model.Camera) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if cancel, exists := m.cameras[cam.ID]; exists {
		cancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cameras[cam.ID] = cancel

	// Kích hoạt tiến trình HLS FFmpeg ngầm cho camera chạy song song cùng luồng đọc AI
	m.hlsServer.StartHLS(ctx, cam.ID.Hex(), cam.RTSPURL)

	go func(ctx context.Context, c model.Camera) {
		log.Printf("[AI/RTSP] Bắt đầu luồng kiểm tra cho camera: %s (%s)\n", c.Name, c.ID.Hex())
		backoff := 1 * time.Second

		for {
			select {
			case <-ctx.Done():
				log.Printf("[RTSP] Dừng camera %s an toàn.\n", c.Name)
				return
			default:
			}

			err := m.mockReadRTSP(c)

			if err != nil {
				log.Printf("[RTSP] Mất kết nối camera %s: %v. Thử lại sau %v\n", c.Name, err, backoff)
				m.updateStatus(c.ID, "offline")
				time.Sleep(backoff)
				if backoff < 30*time.Second {
					backoff *= 2
				}
				continue
			}

			backoff = 1 * time.Second
			m.updateStatus(c.ID, "online")

			if err := m.processFrames(ctx, c); err != nil {
				log.Printf("[RTSP] Stream %s bị đứt đoạn, đang thử khởi động lại.\n", c.Name)
			}
		}
	}(ctx, cam)
}

func (m *Manager) StopStream(camID primitive.ObjectID) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if cancel, exists := m.cameras[camID]; exists {
		cancel()
		delete(m.cameras, camID)
		m.updateStatus(camID, "offline")
	}
}

func (m *Manager) updateStatus(camID primitive.ObjectID, status string) {
	_, err := m.db.Collection("cameras").UpdateOne(context.Background(), bson.M{"_id": camID}, bson.M{"$set": bson.M{"status": status}})
	if err != nil {
		log.Printf("[Manager] CẢNH BÁO: Không thể cập nhật status '%s' cho camera %s: %v\n", status, camID.Hex(), err)
	}
}

func (m *Manager) mockReadRTSP(c model.Camera) error {
	if c.RTSPURL == "" {
		// Giả lập trạng thái luôn Online cho Camera Laptop (không báo mất kết nối nếu URL trống)
		return nil
	}
	return nil
}

func (m *Manager) processFrames(ctx context.Context, c model.Camera) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			log.Printf("[HealthCheck] Camera %s (%s) vẫn đang sống.\n", c.Name, c.ID.Hex())
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}
