package auth

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func getSecretKey() []byte {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// BẮT BUỘC PHẢI CÓ SECRET KEY TRÊN PRODUCTION
		panic("CRITICAL ERROR: JWT_SECRET environment variable is not set! System stopped for security.")
	}
	return []byte(secret)
}

// GenerateToken sinh JWT Token cho một userID cụ thể
func GenerateToken(userID string) (string, error) {
	claims := jwt.MapClaims{
		"userID": userID,
		"exp":    time.Now().Add(time.Hour * 72).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(getSecretKey())
}

func JWTMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		tokenString := ""

		if authHeader != "" {
			parts := strings.Split(authHeader, " ")
			if len(parts) == 2 && parts[0] == "Bearer" {
				tokenString = parts[1]
			}
		}

		// Nếu không có header, chỉ cho phép lấy từ query parameter cho các luồng đặc thù (Stream/WS)
		if tokenString == "" {
			path := c.Request.URL.Path
			if path == "/ws" || (len(path) >= 9 && path[:9] == "/streams/") || (len(path) >= 8 && path[:8] == "/streams") {
				tokenString = c.Query("token")
			}
		}

		if tokenString == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Thiếu JWT Token (Header hoặc Query)"})
			return
		}
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("phương thức ký không hợp lệ: %v", token.Header["alg"])
			}
			return getSecretKey(), nil
		})

		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "JWT Token không hợp lệ"})
			return
		}

		// Trích xuất userID từ claims và lưu vào context của Gin
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			userID, ok := claims["userID"].(string)
			if !ok || userID == "" {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "JWT Token thiếu userID hợp lệ"})
				return
			}
			c.Set("userID", userID)
		}

		c.Next()
	}
}
