package main

import (
	"context"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// main は設定を読み込み、HTTP サーバーを起動する。
func main() {
	port, err := loadPort()
	if err != nil {
		log.Fatal(err)
	}

	router := gin.Default()
	router.Use(corsMiddleware())
	router.GET("/health", healthHandler)

	if err := registerKabuStationRoutes(router); err != nil {
		log.Fatal(err)
	}

	pollingService, err := newKabuPollingService()
	if err != nil {
		log.Fatal(err)
	}
	pollingService.Start(context.Background())

	if err := router.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

// corsMiddleware は Swagger UI やブラウザからの呼び出し向けに CORS と OPTIONS を許可する。
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// healthHandler は稼働確認用のヘルスチェック応答を返す。
func healthHandler(c *gin.Context) {
	sessionState := currentKabuStationSessionState()
	response := gin.H{
		"status":                   "ok",
		"kabus_session_started_at": nil,
	}
	if !sessionState.StartedAt.IsZero() {
		response["kabus_session_started_at"] = sessionState.StartedAt
	}

	c.JSON(http.StatusOK, response)
}
