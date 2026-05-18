package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go-backend/internal/alert"
	"go-backend/internal/analytics"
	"go-backend/internal/auth"
	"go-backend/internal/camera"
	"go-backend/internal/logger"
	"go-backend/internal/mail"
	"go-backend/internal/stream"
	"go-backend/internal/payment"
	"go-backend/internal/billing"
	"go-backend/internal/user"
	"go-backend/internal/ws"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	// Swagger files
	_ "go-backend/docs"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func main() {
	// 0. Initialize Logger
	logger.Init()
	defer logger.Log.Sync()

	// 1. Load environment variables
	if err := godotenv.Load(); err != nil {
		logger.Log.Warn("Lưu ý: Không tìm thấy file .env, sẽ sử dụng biến môi trường hệ thống.")
	}

	// 2. Connect to MongoDB
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		logger.Log.Fatal("Lỗi: MONGODB_URI không được thiết lập trong .env")
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		logger.Log.Fatalf("Lỗi không thể kết nối tới MongoDB: %v", err)
	}
	defer client.Disconnect(context.Background())

	if err := client.Ping(ctx, nil); err != nil {
		logger.Log.Warn("Lưu ý: Không thể ping tới MongoDB. Lỗi:", err)
	}
	db := client.Database("fall_detection")
	logger.Log.Info("✅ Database Init hoàn tất.")

	// 3. Initialize Core Modules
	hub := ws.NewHub()
	go hub.Run()

	hlsServer, err := stream.NewHLSServer()
	if err != nil {
		logger.Log.Fatalf("Lỗi khởi tạo HLS Server: %v", err)
	}
	alertEngine := alert.NewEngine(db, hub, hlsServer)
	alertEngine.Start()

	camManager := camera.NewManager(db, hlsServer)
	camManager.StartAll()

	// 4. Setup HTTP Server
	r := gin.Default()
	
	// CẤU HÌNH CORS AN TOÀN
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000"} // Chỉ cho phép Frontend của bạn
	corsConfig.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization"}
	corsConfig.AllowCredentials = true
	r.Use(cors.New(corsConfig))

	// RATE LIMITER TÙY CHỈNH (Ngăn chặn spam API)
	// Giới hạn mỗi IP tối đa 60 yêu cầu / phút
	rateLimitMap := make(map[string]int)
	var mu sync.RWMutex
	lastReset := time.Now()
	
	r.Use(func(c *gin.Context) {
		ip := c.ClientIP()
		
		mu.Lock()
		if time.Since(lastReset) > time.Minute {
			rateLimitMap = make(map[string]int)
			lastReset = time.Now()
		}
		rateLimitMap[ip]++
		count := rateLimitMap[ip]
		mu.Unlock()

		if count > 60 { // Cho phép 60 req/min
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Bạn đã gửi quá nhiều yêu cầu. Vui lòng thử lại sau 1 phút."})
			c.Abort()
			return
		}
		c.Next()
	})

	// 5. Register Routes
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.Static("/audio", "./audio")
	r.GET("/ws", auth.JWTMiddleware(), func(c *gin.Context) { ws.ServeWs(hub, c) })

	// Handlers
	authHandler := auth.NewHandler(db)
	mailService := mail.NewService()
	userHandler := user.NewHandler(db, mailService, hub)
	billingAPI := billing.NewAPI(db)
	camAPI := camera.NewAPI(db, camManager)
	alertAPI := alert.NewAPI(db, alertEngine)
	paymentHandler := payment.NewHandler(db, hub, mailService)

	// 6. Register Public Routes
	authHandler.RegisterRoutes(r.Group("/api/v1/auth"))
	r.POST("/api/v1/payment/webhook", paymentHandler.SePayWebhook)
	r.POST("/api/v1/bridge/register", camAPI.RegisterBridge)
	// Kết quả inference (Python): xác thực bằng X-API-Key trong handler
	r.POST("/api/v1/ai-result", alertAPI.AIResult)

	// 7. Register Private Routes (Yêu cầu JWT)
	private := r.Group("/api/v1")
	private.Use(auth.JWTMiddleware())
	{
		billingAPI.RegisterRoutes(private)
		camAPI.RegisterRoutes(private)
		private.GET("/incidents", alertAPI.GetIncidents)
		private.POST("/ai/chat", alertAPI.AIChat)
		private.POST("/test-call", alertAPI.TestCall)
		private.POST("/test-adb-push", alertAPI.TestADBPush)
		private.GET("/debug-call-state", alertAPI.DebugCallState)
		private.GET("/health-profiles", userHandler.GetProfile)
		private.PUT("/health-profiles", userHandler.UpdateProfile)
		private.PUT("/health-profiles/contacts", userHandler.UpdateContacts)
		private.PUT("/health-profiles/telegram", userHandler.UpdateTelegramID)
		private.POST("/user/upgrade", userHandler.UpgradePlan)
		private.POST("/user/cancel-plan/request", userHandler.RequestCancelOTP)
		private.POST("/user/cancel-plan", userHandler.CancelPlan)
		private.POST("/user/simulate-payment", userHandler.SimulatePayment)
		private.POST("/ai-models/:id/toggle", alertAPI.ToggleAIModel)
		analytics.RegisterRoutes(private, db)
	}

	r.GET("/api/v1/ai-models", alertAPI.GetAIModels)

	r.GET("/api/v1/user/check-payment", userHandler.CheckPayment)

	// 8. Static Streams (Phục vụ luồng video HLS (Cấu hình không cho phép Cache để tránh lỗi nhảy hình))
	r.GET("/streams/*filepath", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.File(filepath.Join(hlsServer.OutputDir, c.Param("filepath")))
	})

	// 9. Start Server
	logger.Log.Info("🚀 Cardiac Alert Server hiện đang chạy tại cổng :8080")
	if err := r.Run(":8080"); err != nil {
		logger.Log.Fatalf("Server bị crash: %v", err)
	}
}
