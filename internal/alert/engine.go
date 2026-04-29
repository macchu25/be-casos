package alert

import (
	"context"
	"log"
	"sync"
	"time"

	"go-backend/internal/model"
	"go-backend/internal/ws"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type AIResult struct {
	CameraID   primitive.ObjectID
	Label      string
	Confidence float32
}

type CameraState struct {
	SuspectStart   time.Time
	LastAlert      time.Time
	LocalAlertSent bool
}

type Engine struct {
	db       *mongo.Database
	states   map[primitive.ObjectID]*CameraState
	mutex    sync.RWMutex
	ResultCh chan AIResult
	hub      *ws.Hub
}

func NewEngine(db *mongo.Database, hub *ws.Hub) *Engine {
	return &Engine{
		db:       db,
		states:   make(map[primitive.ObjectID]*CameraState),
		ResultCh: make(chan AIResult, 100),
		hub:      hub,
	}
}

func (e *Engine) Start() {
	go func() {
		log.Println("[AlertEngine] Bắt đầu lắng nghe AI results...")
		for result := range e.ResultCh {
			e.Process(result.CameraID, result.Label, result.Confidence)
		}
	}()
}

func (e *Engine) Process(camID primitive.ObjectID, label string, conf float32) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	state, exists := e.states[camID]
	if !exists {
		state = &CameraState{}
		e.states[camID] = state
	}

	if conf > 0.85 && label != "normal" && label != "" {
		if state.SuspectStart.IsZero() {
			state.SuspectStart = time.Now()
			state.LocalAlertSent = false
			log.Printf("[AlertEngine] Camera %s chuyển sang trạng thái THEO DÕI (%s)\n", camID.Hex(), label)
		}

		elapsed := time.Since(state.SuspectStart)

		// GIAI ĐOẠN 1: Cảnh báo tại chỗ (7 giây)
		if elapsed >= 7*time.Second && !state.LocalAlertSent {
			e.triggerLocalWarning(camID, label, conf)
			state.LocalAlertSent = true
		}

		// GIAI ĐOẠN 2: Gọi người thân (8 phút)
		if elapsed >= 8*time.Minute {
			if state.LastAlert.IsZero() || time.Since(state.LastAlert) >= 15*time.Minute {
				e.triggerAlert(camID, label, conf)
				state.LastAlert = time.Now()
			}
		}
	} else {
		if !state.SuspectStart.IsZero() {
			state.SuspectStart = time.Time{}
			state.LocalAlertSent = false
			log.Printf("[AlertEngine] Camera %s trở lại trạng thái NORMAL\n", camID.Hex())
			if e.hub != nil {
				e.hub.Broadcast <- []byte(`{"event":"clear_alert", "camera_id":"` + camID.Hex() + `"}`)
			}
		}
	}
}

func (e *Engine) triggerLocalWarning(camID primitive.ObjectID, label string, conf float32) {
	log.Printf("⚠️ [WARNING] Cảnh báo tại chỗ tại Camera %s (7 giây nằm im)\n", camID.Hex())
	if e.hub != nil {
		e.hub.Broadcast <- []byte(`{"event":"local_warning", "camera_id":"` + camID.Hex() + `", "label":"` + label + `"}`)
	}
	e.pushFCM(camID.Hex(), label)
}

func (e *Engine) triggerAlert(camID primitive.ObjectID, label string, conf float32) {
	log.Printf("🚨 [EMERGENCY] Kích hoạt gọi người thân cho Camera %s (8 phút nằm im)\n", camID.Hex())

	// Tạo Event
	event := model.Event{
		CameraID:        camID,
		Type:            label,
		ConfidenceScore: conf,
		Status:          "active",
		DetectedAt:      time.Now(),
	}

	// Tự động tìm chủ sở hữu của Camera để gán UserID cho Event
	var cameraDoc model.Camera
	err := e.db.Collection("cameras").FindOne(context.Background(), primitive.M{"_id": camID}).Decode(&cameraDoc)
	if err == nil && !cameraDoc.UserID.IsZero() {
		event.UserID = cameraDoc.UserID
	}

	res, err := e.db.Collection("events").InsertOne(context.Background(), event)
	if err != nil {
		log.Printf("[AlertEngine] Lỗi lưu database Event: %v\n", err)
		return
	}
	eventID := res.InsertedID.(primitive.ObjectID)

	// Tạo Alert
	alertRecord := model.Alert{
		EventID:      eventID,
		CameraID:     camID,
		Channel:      "system",
		Recipient:    "115 & admins",
		Status:       "sent",
		SentAt:       time.Now(),
	}
	
	if _, err := e.db.Collection("alerts").InsertOne(context.Background(), alertRecord); err != nil {
		log.Printf("[AlertEngine] Lỗi lưu database Alert: %v\n", err)
	}

	// Đẩy tín hiệu realtime qua WebSockets cho toàn bộ frontend dashboard
	if e.hub != nil {
		e.hub.Broadcast <- []byte(`{"event":"alert", "camera_id":"` + camID.Hex() + `", "label":"` + label + `"}`)
	}

	e.pushFCM(camID.Hex(), label)

	// Tự động gọi người thân qua ElevenLabs
	if !event.UserID.IsZero() {
		go e.initiateElevenLabsCall(event.UserID, label)
	}
}

func (e *Engine) initiateElevenLabsCall(userID primitive.ObjectID, label string) {
	// 1. Tìm hồ sơ y tế của user để lấy danh bạ
	var profile struct {
		Name     string `bson:"name"`
		Contacts []struct {
			Name  string `bson:"name"`
			Phone string `bson:"phone"`
		} `bson:"contacts"`
	}

	coll := e.db.Collection("health_profiles")
	err := coll.FindOne(context.Background(), primitive.M{"user_id": userID}).Decode(&profile)
	if err != nil {
		log.Printf("[AlertEngine] Không tìm thấy hồ sơ y tế cho User %s: %v\n", userID.Hex(), err)
		return
	}

	patientName := profile.Name
	if patientName == "" {
		patientName = "Người thân của bạn"
	}

	// 2. Gọi cho tất cả các số trong danh bạ (hoặc số đầu tiên)
	if len(profile.Contacts) == 0 {
		log.Printf("[AlertEngine] User %s không có danh bạ khẩn cấp.\n", userID.Hex())
		return
	}

	// Chuyển đổi nhãn sang tiếng Việt để Agent nói dễ hiểu hơn
	incidentVN := label
	switch label {
	case "fall":
		incidentVN = "vừa bị ngã"
	case "unconscious":
		incidentVN = "đang bất tỉnh"
	case "seizure":
		incidentVN = "đang bị co giật"
	}

	for _, contact := range profile.Contacts {
		if contact.Phone != "" {
			log.Printf("[ElevenLabs] Đang chuẩn bị gọi cho %s (%s)...\n", contact.Name, contact.Phone)
			err := CallRelative(contact.Phone, patientName, incidentVN)
			if err != nil {
				log.Printf("[ElevenLabs] Lỗi khi gọi cho %s: %v\n", contact.Phone, err)
			}
		}
	}
}

func (e *Engine) callTwilioTTS(camIDHex, label string) {
	log.Printf("📞 [TWILIO TTS] Đang gọi điện 115 cho sự cố %s...\n", label)
}

func (e *Engine) pushFCM(camIDHex, label string) {
	log.Printf("📱 [FCM PUSH] Gửi notification đến mobile: Sự cố %s\n", label)
}

// CallTestManual hỗ trợ nút bấm Test trên giao diện Web
func (e *Engine) CallTestManual(userID primitive.ObjectID) {
	log.Printf("🧪 [TEST] Đang kích hoạt cuộc gọi thử nghiệm cho User %s\n", userID.Hex())
	e.initiateElevenLabsCall(userID, "đây là một cuộc gọi thử nghiệm")
}
