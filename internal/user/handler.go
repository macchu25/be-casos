package user

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-backend/internal/billing"
	"go-backend/internal/mail"
	"go-backend/internal/shared"
	"go-backend/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type UserDoc struct {
	ID                 primitive.ObjectID `bson:"_id" json:"id"`
	Name               string             `bson:"name" json:"name"`
	Email              string             `bson:"email" json:"email"`
	SubscriptionPlan   string             `bson:"subscription_plan" json:"subscription_plan"`
	SubscriptionStatus string             `bson:"subscription_status" json:"subscription_status"`
	PlanExpiresAt      interface{}        `bson:"plan_expires_at" json:"plan_expires_at"`
	LastPaymentAt      interface{}        `bson:"last_payment_at" json:"last_payment_at"`
	LastPaymentRef     string             `bson:"last_payment_ref" json:"last_payment_ref"`
	CancelOTP          string             `bson:"cancel_otp,omitempty" json:"-"`
	CancelOTPExpires   time.Time          `bson:"cancel_otp_expires,omitempty" json:"-"`
}

type Handler struct {
	db   *mongo.Database
	mail *mail.Service
	hub  *ws.Hub
}


func NewHandler(db *mongo.Database, mail *mail.Service, hub *ws.Hub) *Handler {
	return &Handler{db: db, mail: mail, hub: hub}
}

func (h *Handler) GetProfile(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userIDStr, ok := userID.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Session không hợp lệ"})
		return
	}

	objID, _ := primitive.ObjectIDFromHex(userIDStr)
	userColl := h.db.Collection("users")
	
	var user UserDoc
	err := userColl.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&user)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Lấy thông tin sức khỏe (Giữ nguyên logic cũ cho phần này)
	var healthProfile bson.M
	h.db.Collection("health_profiles").FindOne(context.Background(), bson.M{"user_id": objID}).Decode(&healthProfile)

	// Helper để format ngày tháng cực kỳ chắc chắn
	formatDateISO := func(v interface{}) string {
		if v == nil { return "" }
		var t time.Time
		switch val := v.(type) {
		case time.Time: t = val
		case primitive.DateTime: t = val.Time()
		case int64:
			if val > 1e12 { t = time.Unix(0, val*int64(time.Millisecond)) } else { t = time.Unix(val, 0) }
		case float64:
			ts := int64(val)
			if ts > 1e12 { t = time.Unix(0, ts*int64(time.Millisecond)) } else { t = time.Unix(ts, 0) }
		default: return ""
		}
		return t.Format(time.RFC3339)
	}

	formatDateDisplay := func(v interface{}) string {
		iso := formatDateISO(v)
		if iso == "" { return "Vô thời hạn" }
		t, _ := time.Parse(time.RFC3339, iso)
		return t.Format("02/01/2006")
	}

	plan := user.SubscriptionPlan
	status := user.SubscriptionStatus
	if status == "canceled" {
		plan = "free"
		status = "active"
	}

	expiresAtISO := formatDateISO(user.PlanExpiresAt)
	expiresAtDisplay := formatDateDisplay(user.PlanExpiresAt)
	if plan == "free" {
		expiresAtISO = ""
		expiresAtDisplay = "Vô thời hạn"
	}

	// Hợp nhất dữ liệu
	result := gin.H{
		"id":                        user.ID.Hex(),
		"name":                      user.Name,
		"email":                     user.Email,
		"subscription_plan":         plan,
		"subscription_status":       status,
		"plan_expires_at":           expiresAtISO,
		"plan_expires_at_display":   expiresAtDisplay,
		"last_payment_at":           formatDateISO(user.LastPaymentAt),
		"last_payment_ref":          user.LastPaymentRef,
		"age":                       0,
		"location":            "Chưa xác định",
		"bloodType":           "Chưa rõ",
		"conditions":          []string{},
		"contacts":            []interface{}{},
		"lastIncident":        "Chưa có dữ liệu",
		"thrLow":              0.015,
		"thrHigh":             0.040,
		"audioAlert":          true,
		"telegram_chat_id":    "",
	}

	if healthProfile != nil {
		for k, v := range healthProfile {
			if k != "user_id" && k != "_id" && k != "subscription_plan" && k != "subscription_status" && k != "plan_expires_at" {
				result[k] = v
			}
		}
	}

	c.JSON(http.StatusOK, result)
}

func (h *Handler) UpdateContacts(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	var contacts []map[string]interface{}
	if err := c.ShouldBindJSON(&contacts); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
		return
	}

	for _, contact := range contacts {
		if id, ok := contact["id"].(string); !ok || id == "" {
			contact["id"] = uuid.New().String()
		}
	}

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

	coll := h.db.Collection("health_profiles")
	_, err = coll.UpdateOne(
		context.Background(),
		bson.M{"user_id": objID},
		bson.M{"$set": bson.M{"contacts": contacts}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lưu danh bạ"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Thành công", "contacts": contacts})
}

func (h *Handler) UpdateTelegramID(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	var body struct {
		TelegramChatID string `json:"telegram_chat_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
		return
	}

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

	coll := h.db.Collection("health_profiles")
	_, err = coll.UpdateOne(
		context.Background(),
		bson.M{"user_id": objID},
		bson.M{"$set": bson.M{"telegram_chat_id": body.TelegramChatID}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể cập nhật Telegram ID"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Cập nhật Telegram ID thành công"})
}

func (h *Handler) UpdateProfile(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	var body struct {
		Name       string   `json:"name"`
		Age        int      `json:"age"`
		Location   string   `json:"location"`
		BloodType  string   `json:"bloodType"`
		Conditions []string `json:"conditions"`
		ThrLow     float64  `json:"thrLow"`
		ThrHigh    float64  `json:"thrHigh"`
		AudioAlert bool     `json:"audioAlert"`
	}
	if err := h.ShouldBindJSON(c, &body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
		return
	}

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

	coll := h.db.Collection("health_profiles")
	_, err = coll.UpdateOne(
		context.Background(),
		bson.M{"user_id": objID},
		bson.M{"$set": bson.M{
			"name":       body.Name,
			"age":        body.Age,
			"location":   body.Location,
			"bloodType":  body.BloodType,
			"conditions": body.Conditions,
			"thrLow":     body.ThrLow,
			"thrHigh":    body.ThrHigh,
			"audioAlert": body.AudioAlert,
		}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể cập nhật hồ sơ"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Cập nhật hồ sơ thành công"})
}

func (h *Handler) UpgradePlan(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	var body struct {
		Plan string `json:"plan"` // "free", "starter", "creator", "pro", "scale"
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
		return
	}

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

	// Cập nhật gói cước trong collection users
	coll := h.db.Collection("users")
	var user bson.M
	err = coll.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&user)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Không tìm thấy người dùng"})
		return
	}

	planKey := strings.ToLower(strings.TrimSpace(body.Plan))
	paidAt := time.Now().UTC()
	expiresAt := paidAt.AddDate(0, 1, 0)

	setDoc := bson.M{
		"subscription_plan":   planKey,
		"subscription_status": "active",
	}
	if planKey != "free" {
		setDoc["plan_expires_at"] = expiresAt
		setDoc["last_payment_at"] = paidAt
		setDoc["last_payment_ref"] = ""
	}

	_, err = coll.UpdateOne(
		context.Background(),
		bson.M{"_id": objID},
		bson.M{"$set": setDoc},
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể nâng cấp gói cước"})
		return
	}

	ctxDB := context.Background()
	if planKey != "free" {
		_ = billing.RecordPayment(ctxDB, h.db, objID, planKey, "", "manual_upgrade", paidAt, expiresAt)
		_ = billing.InsertSubscriptionActivatedNotification(ctxDB, h.db, objID, planKey, expiresAt, "")
		if h.hub != nil {
			msgData, _ := json.Marshal(map[string]interface{}{
				"type": "subscription_updated",
				"payload": map[string]interface{}{
					"plan":             planKey,
					"status":           "active",
					"plan_expires_at": expiresAt.UTC().Format(time.RFC3339),
				},
			})
			h.hub.Broadcast <- ws.PrivateMessage{UserID: objID.Hex(), Data: msgData}
		}
	}

	userName := "Thành viên"
	if name, ok := user["name"].(string); ok {
		userName = name
	}
	userEmail := ""
	if email, ok := user["email"].(string); ok {
		userEmail = email
	}

	if userEmail != "" && planKey != "free" && h.mail != nil {
		h.mail.SendSubscriptionEmail(userEmail, userName, planKey, paidAt, expiresAt, "")
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Bạn đã đăng ký gói cước thành công",
		"plan":    body.Plan,
	})
}

// CheckPayment checks if a specific payment code has been confirmed
func (h *Handler) CheckPayment(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Mã không hợp lệ"})
		return
	}

	if shared.IsPaymentConfirmed(code) {
		// Clean up after confirmation
		shared.ClearPayment(code)
		c.JSON(http.StatusOK, gin.H{"status": "confirmed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "pending"})
}

// SimulatePayment allows triggering a payment confirmation for demo purposes
func (h *Handler) SimulatePayment(c *gin.Context) {
	code := c.Query("code") // Format: "CASOS SHORTID PLAN"
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Mã không hợp lệ"})
		return
	}

	// 1. Parse the code
	parts := strings.Split(code, " ")
	if len(parts) < 3 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Định dạng mã không hợp lệ"})
		return
	}
	shortID := parts[1]
	plan := strings.ToLower(parts[2])

	// 2. Find user by short ID suffix
	coll := h.db.Collection("users")
	cursor, err := coll.Find(context.Background(), bson.M{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Lỗi cơ sở dữ liệu"})
		return
	}
	defer cursor.Close(context.Background())

	var targetDoc bson.M
	found := false
	for cursor.Next(context.Background()) {
		var u bson.M
		cursor.Decode(&u)
		idStr := u["_id"].(primitive.ObjectID).Hex()
		if strings.HasSuffix(strings.ToUpper(idStr), shortID) {
			targetDoc = u
			found = true
			break
		}
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Không tìm thấy người dùng khớp với mã này"})
		return
	}

	targetUserID := targetDoc["_id"].(primitive.ObjectID)

	ctxDB := context.Background()
	paidAt := time.Now().UTC()
	expiresAt := paidAt.AddDate(0, 1, 0)
	ref := fmt.Sprintf("SIM-%s-%d", strings.ToUpper(shortID), paidAt.UnixNano())

	_, err = coll.UpdateOne(
		ctxDB,
		bson.M{"_id": targetUserID},
		bson.M{"$set": bson.M{
			"subscription_plan":   plan,
			"subscription_status": "active",
			"plan_expires_at":     expiresAt,
			"last_payment_at":     paidAt,
			"last_payment_ref":    ref,
		}},
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể cập nhật gói cước"})
		return
	}

	_ = billing.RecordPayment(ctxDB, h.db, targetUserID, plan, ref, "simulate_payment", paidAt, expiresAt)
	_ = billing.InsertSubscriptionActivatedNotification(ctxDB, h.db, targetUserID, plan, expiresAt, ref)

	userName := "Thành viên"
	if n, ok := targetDoc["name"].(string); ok && n != "" {
		userName = n
	}
	if em, ok := targetDoc["email"].(string); ok && em != "" && h.mail != nil {
		h.mail.SendSubscriptionEmail(em, userName, plan, paidAt, expiresAt, ref)
	}

	if h.hub != nil {
		msgData, _ := json.Marshal(map[string]interface{}{
			"type": "subscription_updated",
			"payload": map[string]interface{}{
				"plan":             plan,
				"status":           "active",
				"plan_expires_at": expiresAt.UTC().Format(time.RFC3339),
			},
		})
		h.hub.Broadcast <- ws.PrivateMessage{UserID: targetUserID.Hex(), Data: msgData}
	}

	shared.ConfirmPayment(code)
	c.JSON(http.StatusOK, gin.H{"message": "Thanh toán tự động thành công cho " + code})
}



func (h *Handler) RequestCancelOTP(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userIDStr, _ := userID.(string)
	objID, _ := primitive.ObjectIDFromHex(userIDStr)

	userColl := h.db.Collection("users")
	var user UserDoc
	if err := userColl.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&user); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	if user.SubscriptionPlan == "free" || user.SubscriptionPlan == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bạn đang dùng gói Free, không cần hủy"})
		return
	}

	// Generate 6-digit OTP
	otp := fmt.Sprintf("%06d", (time.Now().UnixNano()/1000)%1000000)
	expires := time.Now().Add(10 * time.Minute)

	_, err := userColl.UpdateOne(
		context.Background(),
		bson.M{"_id": objID},
		bson.M{"$set": bson.M{
			"cancel_otp":         otp,
			"cancel_otp_expires": expires,
		}},
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể tạo mã xác nhận"})
		return
	}

	if h.mail != nil && user.Email != "" {
		fmt.Printf("📧 [DEBUG] Đang gửi OTP hủy gói tới: %s\n", user.Email)
		_ = h.mail.SendOTPCancelEmail(user.Email, user.Name, otp)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Mã xác nhận đã được gửi về email của bạn"})
}

func (h *Handler) CancelPlan(c *gin.Context) {
	var body struct {
		OTP string `json:"otp"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Vui lòng nhập mã xác nhận"})
		return
	}

	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userIDStr, ok := userID.(string)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Session không hợp lệ"})
		return
	}
	objID, _ := primitive.ObjectIDFromHex(userIDStr)

	userColl := h.db.Collection("users")
	var user UserDoc
	if err := userColl.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&user); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Verify OTP
	if user.CancelOTP == "" || user.CancelOTP != body.OTP {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Mã xác nhận không chính xác"})
		return
	}
	if time.Now().After(user.CancelOTPExpires) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Mã xác nhận đã hết hạn"})
		return
	}

	currentPlan := user.SubscriptionPlan
	if currentPlan == "free" || currentPlan == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bạn đang sử dụng gói Free, không thể hủy"})
		return
	}

	_, err := userColl.UpdateOne(
		context.Background(),
		bson.M{"_id": objID},
		bson.M{
			"$set": bson.M{
				"subscription_plan":   "free",
				"subscription_status": "active",
				"plan_expires_at":     nil,
			},
			"$unset": bson.M{"cancel_otp": "", "cancel_otp_expires": ""},
		},
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể hủy gói cước"})
		return
	}

	_ = billing.InsertSubscriptionCancelledNotification(context.Background(), h.db, objID, currentPlan)
	
	if h.hub != nil {
		msgData, _ := json.Marshal(map[string]interface{}{
			"type": "subscription_updated",
			"payload": map[string]interface{}{
				"plan":   "free",
				"status": "active",
			},
		})
		h.hub.Broadcast <- ws.PrivateMessage{UserID: objID.Hex(), Data: msgData}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Đã hủy gói cước thành công"})
}

func (h *Handler) ShouldBindJSON(c *gin.Context, obj interface{}) error {
	return c.ShouldBindJSON(obj)
}
