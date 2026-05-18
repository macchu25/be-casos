package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type Handler struct {
	db *mongo.Database
}

func NewHandler(db *mongo.Database) *Handler {
	return &Handler{db: db}
}

func formatPlanExpiryForJSON(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case primitive.DateTime:
		return t.Time().UTC().Format(time.RFC3339)
	case int64:
		if t > 1e12 {
			return time.Unix(0, t*int64(time.Millisecond)).UTC().Format(time.RFC3339)
		}
		return time.Unix(t, 0).UTC().Format(time.RFC3339)
	case float64:
		ts := int64(t)
		if ts > 1e12 {
			return time.Unix(0, ts*int64(time.Millisecond)).UTC().Format(time.RFC3339)
		}
		return time.Unix(ts, 0).UTC().Format(time.RFC3339)
	default:
		return v
	}
}

func (h *Handler) SocialLogin(c *gin.Context) {
	var body struct {
		Email      string `json:"email"`
		Name       string `json:"name"`
		Provider   string `json:"provider"`
		ProviderID string `json:"provider_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Thông tin không hợp lệ"})
		return
	}

	userColl := h.db.Collection("users")
	var userDoc bson.M

	err := userColl.FindOne(context.Background(), bson.M{"provider_id": body.ProviderID}).Decode(&userDoc)
	var finalID primitive.ObjectID

	if err == mongo.ErrNoDocuments {
		res, err := userColl.InsertOne(context.Background(), bson.M{
			"name":                body.Name,
			"email":               body.Email,
			"provider":            body.Provider,
			"provider_id":         body.ProviderID,
			"subscription_plan":   "free",
			"subscription_status": "active",
			"created_at":          time.Now(),
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể tạo người dùng"})
			return
		}
		finalID = res.InsertedID.(primitive.ObjectID)
		// Re-fetch to get defaults or just assign
		userDoc = bson.M{
			"subscription_plan":   "free",
			"subscription_status": "active",
		}
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Lỗi truy vấn cơ sở dữ liệu"})
		return
	} else {
		// Safely extract finalID without panicking
		if id, ok := userDoc["_id"].(primitive.ObjectID); ok {
			finalID = id
		} else if idStr, ok := userDoc["_id"].(string); ok {
			objID, _ := primitive.ObjectIDFromHex(idStr)
			finalID = objID
		} else {
			finalID = primitive.NewObjectID()
		}
	}

	// Sinh Token JWT thật cho User này
	token, err := GenerateToken(finalID.Hex())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể tạo token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":               token,
		"user_id":             finalID.Hex(),
		"name":                body.Name,
		"subscription_plan":   userDoc["subscription_plan"],
		"subscription_status": userDoc["subscription_status"],
		"plan_expires_at":     formatPlanExpiryForJSON(userDoc["plan_expires_at"]),
	})
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/social-login", h.SocialLogin)
}
