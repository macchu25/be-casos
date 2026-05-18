package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go-backend/internal/billing"
	"go-backend/internal/mail"
	"go-backend/internal/shared"
	"go-backend/internal/ws"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type Handler struct {
	db   *mongo.Database
	hub  *ws.Hub
	mail *mail.Service
}

func NewHandler(db *mongo.Database, hub *ws.Hub, mail *mail.Service) *Handler {
	return &Handler{db: db, hub: hub, mail: mail}
}

func (h *Handler) SePayWebhook(c *gin.Context) {
	var rawBody map[string]interface{}
	if err := c.ShouldBindJSON(&rawBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
		return
	}

	content := ""
	for _, key := range []string{"content", "transaction_content", "description", "memo"} {
		if v, ok := rawBody[key].(string); ok && v != "" {
			content = v
			break
		}
	}

	content = strings.ToUpper(content)
	fmt.Printf("DEBUG: Processing content: [%s]\n", content)

	re := regexp.MustCompile(`CASOS\s*([A-F0-9]{6})\s*([A-Z]+)`)
	matches := re.FindStringSubmatch(content)

	var shortID, plan string
	if len(matches) >= 3 {
		shortID = matches[1]
		plan = strings.ToLower(matches[2])
	} else {
		if strings.Contains(content, "CASOS") {
			idx := strings.Index(content, "CASOS")
			remaining := content[idx+5:]
			remaining = strings.ReplaceAll(remaining, " ", "")
			if len(remaining) >= 6 {
				shortID = remaining[:6]
				for _, p := range []string{"STARTER", "CREATOR", "PRO", "SCALE"} {
					if strings.Contains(remaining, p) {
						plan = strings.ToLower(p)
						break
					}
				}
			}
		}
	}

	if shortID == "" {
		c.JSON(http.StatusOK, gin.H{"message": "Không tìm thấy mã hợp lệ"})
		return
	}

	userColl := h.db.Collection("users")
	cursor, err := userColl.Find(context.Background(), bson.M{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB error"})
		return
	}
	defer cursor.Close(context.Background())

	var targetUser bson.M
	found := false
	for cursor.Next(context.Background()) {
		var u bson.M
		cursor.Decode(&u)
		idStr := u["_id"].(primitive.ObjectID).Hex()
		if strings.HasSuffix(strings.ToUpper(idStr), shortID) {
			targetUser = u
			found = true
			break
		}
	}

	if !found {
		c.JSON(http.StatusOK, gin.H{"message": "User not found"})
		return
	}

	targetID := targetUser["_id"].(primitive.ObjectID)

	if plan == "" {
		plan = "pro"
	}

	referenceCode, _ := rawBody["referenceCode"].(string)
	if referenceCode == "" {
		if v, ok := rawBody["id"].(string); ok {
			referenceCode = v
		}
	}

	ctxDB := context.Background()
	if billing.PaymentExistsByReference(ctxDB, h.db, targetID, referenceCode) {
		fmt.Printf("INFO: Duplicate webhook skipped ref=%s user=%s\n", referenceCode, targetID.Hex())
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "already_processed"})
		return
	}

	paidAt := time.Now()
	expiresAt := paidAt.AddDate(0, 1, 0)

	_, err = userColl.UpdateOne(
		ctxDB,
		bson.M{"_id": targetID},
		bson.M{"$set": bson.M{
			"subscription_plan":   plan,
			"subscription_status": "active",
			"plan_expires_at":     expiresAt,
			"last_payment_at":     paidAt,
			"last_payment_ref":    referenceCode,
		}},
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Update failed"})
		return
	}

	_ = billing.RecordPayment(ctxDB, h.db, targetID, plan, referenceCode, "sepay_webhook", paidAt, expiresAt)
	_ = billing.InsertSubscriptionActivatedNotification(ctxDB, h.db, targetID, plan, expiresAt, referenceCode)

	userName := "Thành viên"
	if n, ok := targetUser["name"].(string); ok && n != "" {
		userName = n
	}
	if mailStr, ok := targetUser["email"].(string); ok && mailStr != "" && h.mail != nil {
		_ = h.mail.SendSubscriptionEmail(mailStr, userName, plan, paidAt, expiresAt, referenceCode)
	}

	msgData, _ := json.Marshal(map[string]interface{}{
		"type": "subscription_updated",
		"payload": map[string]interface{}{
			"plan":             plan,
			"status":           "active",
			"plan_expires_at": expiresAt.UTC().Format(time.RFC3339),
		},
	})

	h.hub.Broadcast <- ws.PrivateMessage{
		UserID: targetID.Hex(),
		Data:   msgData,
	}

	shared.ConfirmPayment(fmt.Sprintf("CASOS %s %s", shortID, strings.ToUpper(plan)))
	shared.ConfirmPayment(fmt.Sprintf("CASOS%s%s", shortID, strings.ToUpper(plan)))

	fmt.Printf("SUCCESS: Account upgraded for %s to %s\n", shortID, plan)
	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "OK"})
}
