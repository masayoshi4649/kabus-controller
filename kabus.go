package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type symbolStoreRequest struct {
	Symbols []storedRegisterSymbol `json:"symbols"`
}

// registerKabuStationRoutes は KabuS 制御用の HTTP エンドポイントを登録する。
func registerKabuStationRoutes(router *gin.Engine) error {
	service, err := newKabuLoginService()
	if err != nil {
		return err
	}

	wsProxyService, err := newKabuStationWSProxyService()
	if err != nil {
		return err
	}

	router.POST("/kabus/login", service.handleLogin)
	router.POST("/kabus/exit", service.handleExit)
	router.GET("/kabus/symbols", service.handleSymbolsGet)
	router.POST("/kabus/symbols", service.handleSymbolsPost)
	router.GET("/kabusapi/websocket", wsProxyService.handleWebSocketProxy)

	return nil
}

// handleLogin は KabuS の起動・ログイン処理を実行する HTTP ハンドラである。
func (s *kabuLoginService) handleLogin(c *gin.Context) {
	if err := s.TriggerLogin(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"status":  "accepted",
		"message": "KabuS のログイン操作後に APIKey 取得、銘柄登録全解除、定義銘柄の再登録まで実行しました。PID はプログラム内部に保持しています。APIKey はサーバー標準出力へ表示します。",
	})
}

// handleExit は 保持している PID を使って KabuS を終了する HTTP ハンドラである。
func (s *kabuLoginService) handleExit(c *gin.Context) {
	if err := s.Exit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "KabuS の終了を実行しました。",
	})
}

// handleSymbolsGet は登録銘柄 DB の現在内容を返す HTTP ハンドラである。
func (s *kabuLoginService) handleSymbolsGet(c *gin.Context) {
	symbols, err := s.LoadStoredSymbols()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"symbols": symbols,
	})
}

// handleSymbolsPost は登録銘柄 DB の内容を受信データで置き換える HTTP ハンドラである。
func (s *kabuLoginService) handleSymbolsPost(c *gin.Context) {
	var req symbolStoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	if err := s.ReplaceStoredSymbols(req.Symbols); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	syncCtx, cancel := context.WithTimeout(c.Request.Context(), s.timeout+30*time.Second)
	defer cancel()

	if err := s.SyncStoredSymbols(syncCtx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"symbols": req.Symbols,
	})
}
