package camera

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go-backend/internal/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type API struct {
	db      *mongo.Database
	manager *Manager
}

func NewAPI(db *mongo.Database, manager *Manager) *API {
	return &API{db: db, manager: manager}
}

func (a *API) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/cameras", a.GetCameras)
	router.GET("/cameras/discovery", a.DiscoverCameras) // Route mới
	router.POST("/cameras", a.AddCamera)
	router.DELETE("/cameras/:id", a.DeleteCamera)
}

// DiscoverCameras thực hiện quét mạng và trả về danh sách IP camera tìm thấy
func (a *API) DiscoverCameras(c *gin.Context) {
	ips := DiscoverCameras()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"ips":     ips,
		"count":   len(ips),
	})
}

func (a *API) GetCameras(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Không tìm thấy userID"})
		return
	}

	objID, err := primitive.ObjectIDFromHex(userID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID người dùng không hợp lệ"})
		return
	}

	var cams []model.Camera = []model.Camera{}
	cursor, err := a.db.Collection("cameras").Find(context.Background(), bson.M{"user_id": objID})
	if err == nil {
		cursor.All(context.Background(), &cams)
	}
	c.JSON(http.StatusOK, cams)
}

// Xử lý POST (Thêm mới 1 camera hoặc cập nhật nếu trùng ID)
func (a *API) AddCamera(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Không tìm thấy userID"})
		return
	}

	var cam model.Camera
	if err := c.ShouldBindJSON(&cam); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if cam.ID.IsZero() {
		cam.ID = primitive.NewObjectID()
	}
	
	// Gán camera cho người dùng hiện tại
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
	cam.UserID = objID

	// 1. Lấy thông tin gói cước của người dùng
	var user model.User
	err = a.db.Collection("users").FindOne(context.Background(), bson.M{"_id": objID}).Decode(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xác định thông tin gói cước"})
		return
	}

	// 2. Định nghĩa giới hạn cho từng gói
	planLimits := map[string]int64{
		"free":    1,
		"starter": 3,
		"creator": 10,
		"pro":     25,
		"scale":   1000,
	}

	limit, ok := planLimits[user.SubscriptionPlan]
	if !ok {
		limit = 1 // Mặc định là gói Free nếu không xác định được
	}

	// 3. Đếm số camera hiện có của người dùng
	count, err := a.db.Collection("cameras").CountDocuments(context.Background(), bson.M{"user_id": objID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Lỗi khi kiểm tra số lượng camera"})
		return
	}

	// Security: Kiểm tra quyền sở hữu nếu ID camera đã tồn tại
	filter := bson.M{"_id": cam.ID}
	var existingCam model.Camera
	err = a.db.Collection("cameras").FindOne(context.Background(), filter).Decode(&existingCam)
	
	isNewCamera := err != nil // Nếu không tìm thấy thì là camera mới

	// 4. Kiểm tra giới hạn nếu là camera mới
	if isNewCamera && count >= limit {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Giới hạn gói cước",
			"message": fmt.Sprintf("Bạn đã đạt giới hạn tối đa của gói %s (%d camera). Vui lòng nâng cấp để thêm mới.", user.SubscriptionPlan, limit),
		})
		return
	}

	// Nếu camera đã tồn tại và KHÔNG thuộc về user hiện tại -> Từ chối
	if err == nil && existingCam.UserID != objID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Bạn không có quyền cập nhật camera của người khác"})
		return
	}

	opts := options.Update().SetUpsert(true)
	_, err = a.db.Collection("cameras").UpdateOne(context.Background(), filter, bson.M{"$set": cam}, opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	a.manager.StartStream(cam)
	
	c.JSON(http.StatusOK, cam)
}

func (a *API) DeleteCamera(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Không tìm thấy userID"})
		return
	}

	// 1. Chuyển đổi ID Camera
	idStr := c.Param("id")
	id, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID Camera không hợp lệ"})
		return
	}
	
	// 2. Chuyển đổi ID Người dùng từ Token
	userIDStr, _ := userID.(string)
	userObjID, err := primitive.ObjectIDFromHex(userIDStr)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "ID Người dùng không hợp lệ"})
		return
	}

	// 3. Xác minh quyền sở hữu: Thử cả 2 kiểu dữ liệu (ObjectID và String) để tương thích dữ liệu cũ/mới
	filter := bson.M{
		"_id": id,
		"$or": []bson.M{
			{"user_id": userObjID},
			{"user_id": userIDStr},
		},
	}

	var cam model.Camera
	err = a.db.Collection("cameras").FindOne(context.Background(), filter).Decode(&cam)
	
	if err != nil {
		if err == mongo.ErrNoDocuments {
			// Thử tìm camera mà không lọc theo user_id để biết lỗi chính xác
			var existCheck bson.M
			errEx := a.db.Collection("cameras").FindOne(context.Background(), bson.M{"_id": id}).Decode(&existCheck)
			if errEx == mongo.ErrNoDocuments {
				c.JSON(http.StatusNotFound, gin.H{"error": "Không tìm thấy camera này trong hệ thống"})
			} else {
				c.JSON(http.StatusForbidden, gin.H{"error": "Bạn không có quyền sở hữu camera này (Quyền bị từ chối)"})
			}
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Lỗi truy vấn database: " + err.Error()})
		}
		return
	}

	// 4. Dừng stream và xóa
	a.manager.StopStream(id)
	_, err = a.db.Collection("cameras").DeleteOne(context.Background(), bson.M{"_id": id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa camera khỏi database"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Đã chặn stream và xóa camera thành công"})
}

// Xử lý POST đăng ký Bridge URL
func (a *API) RegisterBridge(c *gin.Context) {
	var payload struct {
		UserID string `json:"user_id"`
		URL    string `json:"url"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	objID, err := primitive.ObjectIDFromHex(payload.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user_id"})
		return
	}

	// Cập nhật cấu hình camera là dạng 'bridge'
	// url sẽ lưu stream HLS trực tiếp từ go2rtc qua Cloudflare Tunnel
	streamURL := fmt.Sprintf("%s/api/stream.m3u8?src=camera", payload.URL)

	filter := bson.M{"user_id": objID, "name": "Cardiac Sync Camera"}
	update := bson.M{
		"$set": bson.M{
			"url":         streamURL,
			"type":        "bridge",
			"status":      "online",
			"bridge_url":  payload.URL,
		},
		"$setOnInsert": bson.M{
			"_id":     primitive.NewObjectID(),
			"name":    "Cardiac Sync Camera",
			"user_id": objID,
		},
	}
	opts := options.Update().SetUpsert(true)

	_, err = a.db.Collection("cameras").UpdateOne(context.Background(), filter, update, opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save bridge camera"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Bridge registered successfully", "stream_url": streamURL})
}
