package alert

import (
	"context"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"time"

	"go-backend/internal/cloud"
	"go-backend/internal/metrics"
	"go-backend/internal/model"
	"go-backend/internal/stream"
	"go-backend/internal/telephony"
	"go-backend/internal/ws"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type AIResult struct {
	CameraID   primitive.ObjectID
	ModelName  string
	Label      string
	Confidence float32
}

type Engine struct {
	db        *mongo.Database
	storage   StateStorage
	ResultCh  chan AIResult
	hub       *ws.Hub
	gateway   *telephony.Gateway
	hlsServer *stream.HLSServer
	cloudSync *cloud.SyncManager
}

func NewEngine(db *mongo.Database, hub *ws.Hub, hls *stream.HLSServer) *Engine {
	var storage StateStorage
	redisURL := os.Getenv("REDIS_URL")
	if redisURL != "" {
		storage = NewRedisStorage(redisURL)
	} else {
		storage = NewMemoryStorage()
	}

	engine := &Engine{
		db:        db,
		storage:   storage,
		ResultCh:  make(chan AIResult, 100),
		hub:       hub,
		gateway:   telephony.NewGateway(db),
		hlsServer: hls,
		cloudSync: cloud.NewSyncManager(),
	}

	// LẮNG NGHE LỆNH TỪ TELEGRAM
	go telephony.StartBotListener(func(senderID, action, data string) {
		ctx := context.Background()
		var targetUserID primitive.ObjectID
		if action == "call" {
			id, err := primitive.ObjectIDFromHex(data)
			if err != nil { return }
			targetUserID = id
		} else {
			camID, err := primitive.ObjectIDFromHex(data)
			if err != nil { return }
			var camera model.Camera
			if err := engine.db.Collection("cameras").FindOne(ctx, bson.M{"_id": camID}).Decode(&camera); err != nil {
				return
			}
			targetUserID = camera.UserID
		}

		ownerChatID := engine.getUserChatID(targetUserID)
		if senderID != ownerChatID && senderID != os.Getenv("TELEGRAM_CHAT_ID") {
			telephony.SendTelegramAlertCustom(senderID, "⚠️ Bạn không có quyền điều khiển hệ thống này.", nil)
			return
		}

		switch action {
		case "call":
			var camera model.Camera
			if err := engine.db.Collection("cameras").FindOne(ctx, bson.M{"user_id": targetUserID}).Decode(&camera); err != nil {
				log.Printf("[Bot] Lỗi tìm camera cho user %s: %v\n", targetUserID.Hex(), err)
				return
			}
			engine.gateway.InitiateAndroidCall(targetUserID, camera.ID, "yêu cầu khẩn cấp", telephony.CallRelative, camera.Name)
		case "pause":
			camID, err := primitive.ObjectIDFromHex(data)
			if err != nil { return }
			state, _ := engine.storage.Get(ctx, camID)
			if state != nil {
				state.AlertPaused = true
				engine.storage.Set(ctx, camID, state)
				telephony.SendTelegramAlertCustom(senderID, "✅ Đã dừng báo động lặp lại.", nil)
			}
		case "guide":
			msg := "🤔 <b>Trợ lý Casos:</b> Bạn có đang ở gần nạn nhân không?"
			buttons := telephony.InlineKeyboardMarkup{
				InlineKeyboard: [][]telephony.InlineButton{
					{{Text: "✅ CÓ, TÔI Ở GẦN", CallbackData: "athome_yes:" + data}},
					{{Text: "❌ KHÔNG, TÔI Ở XA", CallbackData: "athome_no:" + data}},
				},
			}
			telephony.SendTelegramAlertCustom(senderID, msg, buttons)
		case "athome_yes":
			telephony.SendTelegramAlertCustom(senderID, "🚑 <b>HÀNH ĐỘNG:</b> Đến ngay hiện trường, giữ nạn nhân nằm yên, gọi 115.", nil)
		case "athome_no":
			telephony.SendTelegramAlertCustom(senderID, "📡 <b>HÀNH ĐỘNG:</b> Gọi hàng xóm, cung cấp địa chỉ cho 115.", nil)
		}
	})

	return engine
}

func (e *Engine) Start() {
	go func() {
		for result := range e.ResultCh {
			e.Process(result.CameraID, result.ModelName, result.Label, result.Confidence)
		}
	}()
}

func (e *Engine) getUserChatID(userID primitive.ObjectID) string {
	var user bson.M
	if err := e.db.Collection("users").FindOne(context.Background(), bson.M{"_id": userID}).Decode(&user); err == nil {
		if chatID, ok := user["telegram_chat_id"].(string); ok { return chatID }
	}
	return os.Getenv("TELEGRAM_CHAT_ID")
}

func (e *Engine) getMedicalSummary(userID primitive.ObjectID) string {
	var profile bson.M
	err := e.db.Collection("health_profiles").FindOne(context.Background(), bson.M{"user_id": userID}).Decode(&profile)
	if err != nil { return "<i>(Chưa có thông tin y tế)</i>" }
	bloodType, _ := profile["blood_type"].(string)
	history, _ := profile["medical_history"].(string)
	if bloodType == "" { bloodType = "Chưa rõ" }
	if history == "" { history = "Không có" }
	return fmt.Sprintf("🩸 <b>Nhóm máu:</b> %s\n💊 <b>Tiền sử:</b> %s", html.EscapeString(bloodType), html.EscapeString(history))
}

func (e *Engine) getMedicalSummaryPlain(userID primitive.ObjectID) string {
	var profile bson.M
	err := e.db.Collection("health_profiles").FindOne(context.Background(), bson.M{"user_id": userID}).Decode(&profile)
	if err != nil { return "Chưa có thông tin y tế" }
	bloodType, _ := profile["blood_type"].(string)
	history, _ := profile["medical_history"].(string)
	if bloodType == "" { bloodType = "Chưa rõ" }
	if history == "" { history = "Không có" }
	return fmt.Sprintf("Nhóm máu: %s. Tiền sử bệnh: %s.", bloodType, history)
}

func (e *Engine) Process(camID primitive.ObjectID, modelName string, label string, conf float32) {
	ctx := context.Background()

	// KIỂM TRA MODEL CÓ ĐANG ACTIVE KHÔNG
	if modelName != "" {
		var aiModel bson.M
		err := e.db.Collection("ai_models").FindOne(ctx, bson.M{"name": modelName}).Decode(&aiModel)
		if err == nil {
			if status, ok := aiModel["status"].(string); ok && status != "Active" {
				// Model bị tắt, bỏ qua kết quả
				return
			}
		}
	}

	state, _ := e.storage.Get(ctx, camID)
	if state == nil { state = &CameraState{} }

	if float64(conf) > 0.85 && label != "normal" && label != "" {
		if state.SuspectStart.IsZero() {
			state.SuspectStart = time.Now()
			state.AlertPaused = false
			metrics.ActiveAlerts.Inc()
			var camera model.Camera
			if err := e.db.Collection("cameras").FindOne(ctx, bson.M{"_id": camID}).Decode(&camera); err != nil {
				log.Printf("[Engine] Lỗi tìm camera %s: %v\n", camID.Hex(), err)
				return
			}
			_, patientName := e.getDetailedInfo(camID, camera.UserID)
			msg := fmt.Sprintf("🔍 <b>[Casos - THEO DÕI]</b>\n👤 <b>Đối tượng:</b> %s\n⚠️ <b>Dấu hiệu:</b> %s", html.EscapeString(patientName), html.EscapeString(label))
			telephony.SendTelegramAlertCustom(e.getUserChatID(camera.UserID), msg, nil)
		}
		if state.AlertPaused { return }
		if time.Since(state.SuspectStart) >= 7*time.Second {
			if !state.LocalAlertSent {
				var camera model.Camera
				if err := e.db.Collection("cameras").FindOne(ctx, bson.M{"_id": camID}).Decode(&camera); err == nil {
					go e.gateway.TriggerLocalAlarm(camera.UserID, camID)
				}
				e.broadcastToOwner(camID, []byte(`{"event":"local_warning", "camera_id":"` + camID.Hex() + `"}`))
				state.LocalAlertSent = true
			}
		}
		if time.Since(state.SuspectStart) >= 10*time.Second {
			if state.LastAlert.IsZero() || time.Since(state.LastAlert) >= 5*time.Minute {
				e.triggerAlert(camID, label, conf)
				state.LastAlert = time.Now()
			}
		}
		e.storage.Set(ctx, camID, state)
	} else {
		if !state.SuspectStart.IsZero() {
			state.SuspectStart = time.Time{}
			state.AlertPaused = false
			state.LocalAlertSent = false
			metrics.ActiveAlerts.Dec()
			e.broadcastToOwner(camID, []byte(`{"event":"clear_alert", "camera_id":"` + camID.Hex() + `"}`))
			e.storage.Set(ctx, camID, state)
		}
	}
}

func (e *Engine) triggerAlert(camID primitive.ObjectID, label string, conf float32) {
	var cameraDoc model.Camera
	if err := e.db.Collection("cameras").FindOne(context.Background(), bson.M{"_id": camID}).Decode(&cameraDoc); err != nil {
		log.Printf("[Engine] Lỗi triggerAlert: không tìm thấy camera %s: %v\n", camID.Hex(), err)
		return
	}
	camName, patientName := e.getDetailedInfo(camID, cameraDoc.UserID)
	chatID := e.getUserChatID(cameraDoc.UserID)
	medical := e.getMedicalSummary(cameraDoc.UserID)

	msg := fmt.Sprintf("🚨 <b>[Casos - KHẨN CẤP]</b>\n🆘 <b>SỰ CỐ:</b> %s\n👤 <b>Nạn nhân:</b> %s\n📍 <b>Tại:</b> %s\n📋 <b>HỒ SƠ Y TẾ:</b>\n%s", html.EscapeString(label), html.EscapeString(patientName), html.EscapeString(camName), medical)
	buttons := telephony.InlineKeyboardMarkup{
		InlineKeyboard: [][]telephony.InlineButton{
			{{Text: "📖 HƯỚNG DẪN XỬ LÝ", CallbackData: "guide:" + camID.Hex()}},
			{{Text: "⏹️ TẠM DỪNG BÁO ĐỘNG", CallbackData: "pause:" + camID.Hex()}},
			{{Text: "⚡ GỌI LẠI KHẨN CẤP", CallbackData: "call:" + cameraDoc.UserID.Hex()}},
		},
	}
	go telephony.SendTelegramAlertCustom(chatID, msg, buttons)
	
	imgData, err := os.ReadFile("audio/mockup.png")
	if err == nil {
		go telephony.SendTelegramPhotoCustom(chatID, "🚨 BẰNG CHỨNG", imgData, buttons)
	} else {
		log.Printf("[Engine] Không thể đọc ảnh bằng chứng: %v\n", err)
	}
	metrics.EmergencyCalls.Inc()
	go e.gateway.InitiateAndroidCall(cameraDoc.UserID, camID, label, telephony.CallRelative, camName)

	// HYBRID CLOUD: Đẩy bằng chứng lên S3/Firebase
	go func() {
		e.cloudSync.UploadIncidentEvidence("audio/mockup.png")
	}()

	// ─── LƯU INCIDENT VÀO VECTOR DB ───
	go func() {
		incidentText := fmt.Sprintf("Phát hiện sự cố %s tại %s của bệnh nhân %s vào lúc %s.", label, camName, patientName, time.Now().Format("15:04:05 02/01/2006"))
		
		// 1. Lưu vào MongoDB collection 'events'
		event := bson.M{
			"user_id":     cameraDoc.UserID,
			"camera_id":   camID,
			"type":        label,
			"description": incidentText,
			"created_at":  time.Now(),
		}
		e.db.Collection("events").InsertOne(context.Background(), event)

		// 2. Lưu vào AI Vector DB
		payload := map[string]interface{}{
			"id":       primitive.NewObjectID().Hex(),
			"text":     incidentText,
			"metadata": map[string]string{"user_id": cameraDoc.UserID.Hex(), "type": label},
		}
		pbody, _ := json.Marshal(payload)
		http.Post("http://localhost:8001/index", "application/json", bytes.NewBuffer(pbody))
	}()
}

func (e *Engine) getDetailedInfo(camID, userID primitive.ObjectID) (string, string) {
	camName := camID.Hex()
	var camera model.Camera
	if err := e.db.Collection("cameras").FindOne(context.Background(), bson.M{"_id": camID}).Decode(&camera); err != nil {
		log.Printf("[Engine] Lỗi getDetailedInfo cho camera %s: %v\n", camID.Hex(), err)
	}
	if camera.Name != "" { camName = camera.Name }
	patientName := "Người thân"
	var profile bson.M
	if err := e.db.Collection("health_profiles").FindOne(context.Background(), bson.M{"user_id": userID}).Decode(&profile); err == nil {
		if name, ok := profile["name"].(string); ok { patientName = name }
	}
	return camName, patientName
}

func (e *Engine) broadcastToOwner(camID primitive.ObjectID, data []byte) {
	if e.hub == nil { return }
	var cameraDoc model.Camera
	if err := e.db.Collection("cameras").FindOne(context.Background(), bson.M{"_id": camID}).Decode(&cameraDoc); err != nil {
		return
	}
	if !cameraDoc.UserID.IsZero() {
		e.hub.Broadcast <- ws.PrivateMessage{UserID: cameraDoc.UserID.Hex(), Data: data}
	}
}

func (e *Engine) CallTestManual(userID primitive.ObjectID, specificPhone string) {
	var camera model.Camera
	if err := e.db.Collection("cameras").FindOne(context.Background(), bson.M{"user_id": userID}).Decode(&camera); err != nil {
		log.Printf("[Engine] Lỗi CallTestManual: không có camera cho user %s: %v\n", userID.Hex(), err)
		return
	}
	camName, patientName := e.getDetailedInfo(camera.ID, userID)
	msg := fmt.Sprintf("🚨 <b>[Casos - TEST]</b>\n👤 <b>Nạn nhân:</b> %s\n📍 <b>Tại:</b> %s", patientName, camName)
	buttons := telephony.InlineKeyboardMarkup{
		InlineKeyboard: [][]telephony.InlineButton{
			{{Text: "📖 HƯỚNG DẪN XỬ LÝ", CallbackData: "guide:" + camera.ID.Hex()}},
			{{Text: "⏹️ TẠM DỪNG TEST", CallbackData: "pause:" + camera.ID.Hex()}},
			{{Text: "⚡ GỌI TEST LẠI", CallbackData: "call:" + userID.Hex()}},
		},
	}
	go telephony.SendTelegramAlertCustom(e.getUserChatID(userID), msg, buttons)
	e.gateway.InitiateAndroidCall(userID, camera.ID, "đang bị ngã", telephony.CallRelative, camName, specificPhone)
}
