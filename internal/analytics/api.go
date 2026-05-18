package analytics

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type AnalyticsHandler struct {
	db *mongo.Database
}

func RegisterRoutes(r *gin.RouterGroup, db *mongo.Database) {
	h := &AnalyticsHandler{db: db}
	group := r.Group("/analytics")
	{
		group.GET("/summary", h.GetSummary)
		group.GET("/timeline", h.GetTimeline)
	}
}

func (h *AnalyticsHandler) GetSummary(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Thống kê tổng quát
	totalIncidents, _ := h.db.Collection("events").CountDocuments(ctx, bson.M{})
	last24h := time.Now().Add(-24 * time.Hour)
	recentIncidents, _ := h.db.Collection("events").CountDocuments(ctx, bson.M{
		"detected_at": bson.M{"$gte": last24h},
	})
	activeCameras, _ := h.db.Collection("cameras").CountDocuments(ctx, bson.M{})

	// 2. PHÂN LOẠI SỰ CỐ THỰC TẾ (Cho biểu đồ tròn)
	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.M{
			"_id":   "$label",
			"count": bson.M{"$sum": 1},
		}}},
	}
	cursor, _ := h.db.Collection("events").Aggregate(ctx, pipeline)
	
	type Category struct {
		Label string  `json:"label"`
		Count int64   `json:"count"`
		Percent float64 `json:"percent"`
	}
	var categories []Category
	var results []bson.M
	cursor.All(ctx, &results)

	for _, res := range results {
		label, _ := res["_id"].(string)
		count := int64(res["count"].(int32))
		if label == "" { label = "Khác" }
		
		percent := 0.0
		if totalIncidents > 0 {
			percent = (float64(count) / float64(totalIncidents)) * 100
		}
		categories = append(categories, Category{Label: label, Count: count, Percent: percent})
	}

	c.JSON(http.StatusOK, gin.H{
		"total_incidents":  totalIncidents,
		"recent_24h":       recentIncidents,
		"active_cameras":   activeCameras,
		"categories":       categories, // Dữ liệu thật cho Pie Chart
		"system_health":    "Excellent",
	})
}

func (h *AnalyticsHandler) GetTimeline(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sevenDaysAgo := time.Now().AddDate(0, 0, -7)
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"detected_at": bson.M{"$gte": sevenDaysAgo}}}},
		{{Key: "$group", Value: bson.M{
			"_id": bson.M{"$dateToString": bson.M{"format": "%Y-%m-%d", "date": "$detected_at"}},
			"count": bson.M{"$sum": 1},
		}}},
		{{Key: "$sort", Value: bson.M{"_id": 1}}},
	}
	cursor, _ := h.db.Collection("events").Aggregate(ctx, pipeline)
	results := []bson.M{}
	if err := cursor.All(ctx, &results); err != nil {
		results = []bson.M{}
	}
	c.JSON(http.StatusOK, results)
}
