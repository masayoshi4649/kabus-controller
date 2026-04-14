package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	kabusapi "github.com/masayoshi4649/kabus-api"
)

// symbolStoreRequest は登録銘柄更新 API のリクエストボディを表す。
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
	router.GET("/future/products", handleFutureProductsGet)
	router.GET("/future/wallet", handleFutureWalletGet)
	router.GET("/future/orders", handleFutureOrdersGet)
	router.GET("/future/positions", handleFuturePositionsGet)
	router.GET("/future/symbolname", handleFutureSymbolNameGet)
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

// handleFutureProductsGet は先物商品コード一覧を返す HTTP ハンドラである。
func handleFutureProductsGet(c *gin.Context) {
	c.JSON(http.StatusOK, FutureProducts)
}

// handleFutureWalletGet はポーリングで保持している先物余力を返す HTTP ハンドラである。
func handleFutureWalletGet(c *gin.Context) {
	c.JSON(http.StatusOK, currentKabuPollingFutureWallet())
}

// handleFutureOrdersGet はポーリングで保持している先物注文一覧を返す HTTP ハンドラである。
func handleFutureOrdersGet(c *gin.Context) {
	c.JSON(http.StatusOK, currentKabuPollingOrders())
}

// handleFuturePositionsGet はポーリングで保持している先物建玉一覧を返す HTTP ハンドラである。
func handleFuturePositionsGet(c *gin.Context) {
	c.JSON(http.StatusOK, currentKabuPollingPositions())
}

// handleFutureSymbolNameGet は保持済み APIKey を使って先物銘柄コード情報を取得する HTTP ハンドラである。
func handleFutureSymbolNameGet(c *gin.Context) {
	sessionState := currentKabuStationSessionState()
	apiKey := strings.TrimSpace(sessionState.APIKey)
	if apiKey == "" {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "APIKey が保持されていません。先に /kabus/login を実行してください",
		})
		return
	}

	futureCode := strings.TrimSpace(c.Query("FutureCode"))
	if futureCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "FutureCode は必須です",
		})
		return
	}

	derivMonthText := strings.TrimSpace(c.DefaultQuery("DerivMonth", "0"))
	derivMonth, err := strconv.Atoi(derivMonthText)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "DerivMonth は整数で指定してください",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	client := kabusapi.NewClient(kabusapi.Config{Token: apiKey})
	defer client.CloseIdleConnections()

	response, err := client.GetFutureSymbolName(ctx, kabusapi.FutureSymbolNameOptions{
		FutureCode: futureCode,
		DerivMonth: derivMonth,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, response)
}
