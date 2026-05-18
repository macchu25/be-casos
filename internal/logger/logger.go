package logger

import (
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
)

var Log *zap.SugaredLogger

func Init() {
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	
	// Set log level from env if needed
	level := zap.NewAtomicLevelAt(zap.InfoLevel)
	if os.Getenv("DEBUG") == "true" {
		level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}
	config.Level = level

	logger, err := config.Build()
	if err != nil {
		// Fallback nếu không thể build logger (hiếm gặp)
		fmt.Printf("CẢNH BÁO: Không thể khởi tạo Zap Logger: %v. Dùng mặc định.\n", err)
		Log = zap.NewExample().Sugar()
		return
	}
	Log = logger.Sugar()
}
