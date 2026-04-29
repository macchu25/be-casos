package camera

import (
	"context"
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
	router.POST("/cameras", a.AddCamera)
	router.DELETE("/cameras/:id", a.DeleteCamera)
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
	objID, _ := primitive.ObjectIDFromHex(userID.(string))
	cam.UserID = objID

	opts := options.Update().SetUpsert(true)
	_, err := a.db.Collection("cameras").UpdateOne(context.Background(), bson.M{"_id": cam.ID}, bson.M{"$set": cam}, opts)
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
