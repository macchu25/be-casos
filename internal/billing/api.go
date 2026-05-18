package billing

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	collUsers         = "users"
	collPayments      = "payments"
	collNotifications = "billing_notifications"
)

type API struct {
	db *mongo.Database
}

func NewAPI(db *mongo.Database) *API {
	return &API{db: db}
}

func userObjectID(c *gin.Context) (primitive.ObjectID, bool) {
	userIDRaw, exists := c.Get("userID")
	if !exists {
		return primitive.NilObjectID, false
	}
	userIDStr, ok := userIDRaw.(string)
	if !ok {
		return primitive.NilObjectID, false
	}
	oid, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		return primitive.NilObjectID, false
	}
	return oid, true
}

func (a *API) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/billing/payments", a.ListPayments)
	group.GET("/notifications", a.ListNotifications)
	group.POST("/notifications/mark-read", a.MarkNotificationsRead)
}

func (a *API) ListPayments(c *gin.Context) {
	uid, ok := userObjectID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	ctx := context.Background()
	cur, err := a.db.Collection(collPayments).Find(ctx, bson.M{"user_id": uid}, options.Find().
		SetSort(bson.M{"paid_at": -1}).
		SetLimit(100))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không đọc được lịch sử"})
		return
	}
	defer cur.Close(ctx)

	var out []gin.H
	for cur.Next(ctx) {
		var raw bson.M
		if cur.Decode(&raw) != nil {
			continue
		}
		id, _ := raw["_id"].(primitive.ObjectID)
		paidAt := formatBSONTime(raw["paid_at"])
		expires := formatBSONTime(raw["plan_expires_at"])
		ref, _ := raw["reference_code"].(string)
		plan, _ := raw["plan"].(string)
		src, _ := raw["source"].(string)
		out = append(out, gin.H{
			"id":              id.Hex(),
			"plan":            plan,
			"reference_code": ref,
			"paid_at":         paidAt,
			"plan_expires_at": expires,
			"source":          src,
		})
	}

	if len(out) == 0 {
		var u bson.M
		if err := a.db.Collection(collUsers).FindOne(ctx, bson.M{"_id": uid}).Decode(&u); err == nil {
			if plan, ok := u["subscription_plan"].(string); ok && plan != "" && plan != "free" {
				paidAt := formatBSONTime(u["last_payment_at"])
				expires := formatBSONTime(u["plan_expires_at"])
				ref, _ := u["last_payment_ref"].(string)
				if paidAt != "" {
					out = append(out, gin.H{
						"id":              uid.Hex() + "-legacy",
						"plan":            plan,
						"reference_code": ref,
						"paid_at":         paidAt,
						"plan_expires_at": expires,
						"source":          "legacy_record",
					})
				}
			}
		}
	}

	if out == nil {
		out = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"payments": out})
}

func (a *API) ListNotifications(c *gin.Context) {
	uid, ok := userObjectID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	ctx := context.Background()
	cur, err := a.db.Collection(collNotifications).Find(ctx, bson.M{"user_id": uid}, options.Find().
		SetSort(bson.M{"created_at": -1}).
		SetLimit(50))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không đọc được thông báo"})
		return
	}
	defer cur.Close(ctx)

	var unread int64

	var items []gin.H
	for cur.Next(ctx) {
		var raw bson.M
		if cur.Decode(&raw) != nil {
			continue
		}
		id, _ := raw["_id"].(primitive.ObjectID)
		kind, _ := raw["kind"].(string)
		title, _ := raw["title"].(string)
		body, _ := raw["body"].(string)
		rd, _ := raw["read"].(bool)
		if !rd {
			unread++
		}
		created := formatBSONTime(raw["created_at"])
		items = append(items, gin.H{
			"id":          id.Hex(),
			"kind":        kind,
			"title":       title,
			"body":        body,
			"read":        rd,
			"created_at": created,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"notifications": items,
		"unread_count": unread,
	})
}

type markReadBody struct {
	All bool     `json:"all"`
	IDs []string `json:"ids"`
}

func (a *API) MarkNotificationsRead(c *gin.Context) {
	uid, ok := userObjectID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var body markReadBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
		return
	}

	filter := bson.M{"user_id": uid}
	if body.All {
		// chỉ filter user_id
	} else if len(body.IDs) > 0 {
		oids := make([]primitive.ObjectID, 0, len(body.IDs))
		for _, s := range body.IDs {
			oid, err := primitive.ObjectIDFromHex(s)
			if err == nil {
				oids = append(oids, oid)
			}
		}
		if len(oids) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Không có id hợp lệ"})
			return
		}
		filter["_id"] = bson.M{"$in": oids}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Truyền all: true hoặc ids"})
		return
	}

	ctx := context.Background()
	_, err := a.db.Collection(collNotifications).UpdateMany(ctx, filter, bson.M{
		"$set": bson.M{"read": true},
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Cập nhật thất bại"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func formatBSONTime(v interface{}) string {
	switch t := v.(type) {
	case time.Time:
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	case primitive.DateTime:
		if t == 0 {
			return ""
		}
		return t.Time().UTC().Format(time.RFC3339)
	default:
		return ""
	}
}
