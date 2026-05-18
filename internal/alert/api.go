package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

func execADB(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	adbExe := os.Getenv("ADB_PATH")
	if adbExe == "" {
		adbExe = `C:\adb\adb.exe`
	}
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, adbExe, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}


type API struct {
	db     *mongo.Database
	engine *Engine
}

func NewAPI(db *mongo.Database, engine *Engine) *API {
	return &API{db: db, engine: engine}
}

func (a *API) GetIncidents(c *gin.Context) {
	collection := a.db.Collection("events")
	
	userID, _ := c.Get("userID")
	userIDStr, ok := userID.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Session không hợp lệ"})
		return
	}
	objID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID người dùng không hợp lệ"})
		return
	}
	filter := bson.M{"user_id": objID}

	cursor, err := collection.Find(context.Background(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lấy dữ liệu sự cố"})
		return
	}
	var events []interface{}
	if err = cursor.All(context.Background(), &events); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Lỗi parse dữ liệu"})
		return
	}
	c.JSON(http.StatusOK, events)
}

func (a *API) AIChat(c *gin.Context) {
	var payload struct {
		Query string `json:"query"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
		return
	}

	// Gọi đến AI Python service
	pbody, _ := json.Marshal(map[string]string{"query": payload.Query})
	resp, err := http.Post("http://localhost:8001/chat", "application/json", bytes.NewBuffer(pbody))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI service hiện không khả dụng"})
		return
	}
	defer resp.Body.Close()

	var result gin.H
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		c.JSON(http.StatusOK, result)
	} else {
		c.JSON(http.StatusOK, gin.H{
			"answer": "Hệ thống AI đang xử lý dữ liệu. Vui lòng thử lại sau.",
		})
	}
}

func (a *API) TestCall(c *gin.Context) {
	userID, _ := c.Get("userID")
	userIDStr, ok := userID.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Session không hợp lệ"})
		return
	}
	objID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID người dùng không hợp lệ"})
		return
	}
	
	var payload struct {
		Phone string `json:"phone"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
		return
	}
	
	a.engine.CallTestManual(objID, payload.Phone)
	c.JSON(http.StatusOK, gin.H{"message": "Đã kích hoạt cuộc gọi thử nghiệm"})
}

func (a *API) TestADBPush(c *gin.Context) {
	_, ok := c.Get("userID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Session không hợp lệ"})
		return
	}

	result := gin.H{}

	// 1. Kiểm tra thiết bị
	out, err := execADB("devices")
	result["devices_output"] = out
	if err != nil {
		result["devices_error"] = err.Error()
		result["connected"] = false
		c.JSON(http.StatusOK, result)
		return
	}
	result["connected"] = strings.Contains(out, "\tdevice")

	// 2. Thử push file
	// Tìm file audio mới nhất trong thư mục audio/
	matches, globErr := filepath.Glob("audio/alert_*.mp3")
	audioFile := "audio/emergency_vi.mp3" // fallback
	if globErr == nil && len(matches) > 0 {
		sort.Strings(matches)
		audioFile = matches[len(matches)-1]
	}
	result["audio_file_used"] = audioFile

	pushOut, pushErr := execADB("push", audioFile, "/sdcard/alert.mp3")
	result["push_output"] = pushOut
	if pushErr != nil {
		result["push_error"] = pushErr.Error()
		result["push_success"] = false
	} else {
		result["push_success"] = true
	}

	// 3. Kiểm tra file đã tồn tại chưa
	lsOut, _ := execADB("shell", "ls", "-lh", "/sdcard/alert.mp3")
	result["file_check"] = lsOut

	// 4. Tăng âm lượng tối đa
	execADB("shell", "media", "volume", "--set", "15", "--stream", "4", "--show")
	execADB("shell", "media", "volume", "--set", "15", "--stream", "3", "--show")

	// 5. Phát âm thanh
	playOut, playErr := execADB("shell",
		"am", "start", "-W",
		"-a", "android.intent.action.VIEW",
		"-d", "file:///sdcard/alert.mp3",
		"-t", "audio/mpeg",
	)
	result["play_output"] = playOut
	if playErr != nil {
		result["play_error"] = playErr.Error()
		result["play_success"] = false

		// Fallback: thử audio/*
		playOut2, playErr2 := execADB("shell",
			"am", "start", "-W",
			"-a", "android.intent.action.VIEW",
			"-d", "file:///sdcard/alert.mp3",
			"-t", "audio/*",
		)
		result["play_fallback_output"] = playOut2
		if playErr2 != nil {
			result["play_fallback_error"] = playErr2.Error()
		} else {
			result["play_success"] = true
		}
	} else {
		result["play_success"] = true
	}

	c.JSON(http.StatusOK, result)
}

func (a *API) DebugCallState(c *gin.Context) {
	_, ok := c.Get("userID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Lấy toàn bộ output của dumpsys telephony.registry
	raw, err := execADB("shell", "dumpsys", "telephony.registry")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error(), "raw": raw})
		return
	}

	// Lọc chỉ lấy những dòng liên quan đến CallState
	var relevant []string
	for _, line := range strings.Split(raw, "\n") {
		l := strings.ToLower(line)
		if strings.Contains(l, "callstate") || strings.Contains(l, "offhook") || strings.Contains(l, "ringing") {
			relevant = append(relevant, strings.TrimSpace(line))
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"relevant_lines": relevant,
		"raw_snippet":    raw[:min(len(raw), 3000)], // 3000 ký tự đầu
	})
}

func min(a, b int) int {
	if a < b { return a }
	return b
}


func (a *API) AIResult(c *gin.Context) {
	// Security check: Verify Internal API Key
	apiKey := c.GetHeader("X-API-Key")
	expectedKey := os.Getenv("INTERNAL_API_KEY")
	// BẮT BUỘC PHẢI CÓ KEY ĐỂ TRÁNH FAKE INCIDENT
	if expectedKey == "" || apiKey != expectedKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Invalid or Missing API Key"})
		return
	}

	var payload struct {
		CameraID   string  `json:"CameraID"`
		ModelName  string  `json:"ModelName"`
		Label      string  `json:"Label"`
		Confidence float32 `json:"Confidence"`
	}
	if err := c.ShouldBindJSON(&payload); err == nil {
		camID, err := primitive.ObjectIDFromHex(payload.CameraID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ID Camera không hợp lệ"})
			return
		}
		a.engine.ResultCh <- AIResult{
			CameraID:   camID,
			ModelName:  payload.ModelName,
			Label:      payload.Label,
			Confidence: payload.Confidence,
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Định dạng dữ liệu không hợp lệ"})
	}
}

func (a *API) GetAIModels(c *gin.Context) {
	// Cho phép truy cập qua JWT hoặc X-API-Key (cho script Python)
	apiKey := c.GetHeader("X-API-Key")
	expectedKey := os.Getenv("INTERNAL_API_KEY")
	
	_, hasJWT := c.Get("userID")
	
	if !hasJWT && (expectedKey == "" || apiKey != expectedKey) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	collection := a.db.Collection("ai_models")
	cursor, err := collection.Find(context.Background(), bson.M{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lấy danh sách model"})
		return
	}
	var models []bson.M
	if err = cursor.All(context.Background(), &models); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Lỗi parse dữ liệu"})
		return
	}

	// Nếu chưa có model nào, tạo mặc định
	if len(models) == 0 {
		defaultModels := []interface{}{
			bson.M{
				"name": "Fall Detection Engine",
				"version": "2.1.0",
				"type": "CNN-LSTM + MediaPipe",
				"status": "Active",
				"precision": "85.0%",
				"latency": "25ms",
			},
			bson.M{
				"name": "Human Pose Estimation",
				"version": "1.4.2",
				"type": "MediaPipe",
				"status": "Active",
				"precision": "94.2%",
				"latency": "24ms",
			},
			bson.M{
				"name": "YOLO Furniture Detector",
				"version": "1.0.0",
				"type": "YOLOv11-Nano",
				"status": "Idle",
				"precision": "92.0%",
				"latency": "15ms",
			},
		}
		collection.InsertMany(context.Background(), defaultModels)
		// Lấy lại sau khi insert
		cursor, _ = collection.Find(context.Background(), bson.M{})
		cursor.All(context.Background(), &models)
	}

	c.JSON(http.StatusOK, models)
}

func (a *API) ToggleAIModel(c *gin.Context) {
	id := c.Param("id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID không hợp lệ"})
		return
	}

	collection := a.db.Collection("ai_models")
	var model bson.M
	if err := collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&model); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Không tìm thấy model"})
		return
	}

	newStatus := "Active"
	if model["status"] == "Active" {
		newStatus = "Idle"
	}

	_, err = collection.UpdateOne(
		context.Background(),
		bson.M{"_id": objID},
		bson.M{"$set": bson.M{"status": newStatus}},
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể cập nhật trạng thái"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "new_status": newStatus})
}

func (a *API) Answer(c *gin.Context) {
	scco := []gin.H{
		{
			"action": "talk",
			"text":   "Chào bạn, đây là thông báo khẩn cấp từ hệ thống Cardiac Alert. Người thân của bạn đang gặp sự cố, vui lòng kiểm tra ngay lập tức.",
			"voice":  "female",
			"speed":  0,
		},
	}
	c.JSON(http.StatusOK, scco)
}
