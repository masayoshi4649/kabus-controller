package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// wsProxyBufferSize は WebSocket フレーム転送時に使うバッファサイズである。
const wsProxyBufferSize = 32 * 1024

// wsProxyHandshakeTimeout は上流 WebSocket とのハンドシェイク待機時間である。
const wsProxyHandshakeTimeout = 10 * time.Second

// wsProxyCloseWriteTimeout は Close フレーム転送時の書き込み期限である。
const wsProxyCloseWriteTimeout = 5 * time.Second

// kabuStationWSProxyService は KabuS のローカル WebSocket を外部公開用に中継する。
type kabuStationWSProxyService struct {
	upstreamURL *url.URL
	dialer      websocket.Dialer
	upgrader    websocket.Upgrader
}

// newKabuStationWSProxyService は WebSocket 中継サービスを初期化する。
func newKabuStationWSProxyService() (*kabuStationWSProxyService, error) {
	rawURL, err := loadKabuStationWebSocketURL()
	if err != nil {
		return nil, err
	}

	upstreamURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("KabuS WebSocket URL の解析に失敗しました: %w", err)
	}

	if upstreamURL.Scheme != "ws" && upstreamURL.Scheme != "wss" {
		return nil, fmt.Errorf("KabuS WebSocket URL は ws または wss である必要があります: %s", rawURL)
	}

	return &kabuStationWSProxyService{
		upstreamURL: upstreamURL,
		dialer: websocket.Dialer{
			Proxy:             http.ProxyFromEnvironment,
			HandshakeTimeout:  wsProxyHandshakeTimeout,
			ReadBufferSize:    wsProxyBufferSize,
			WriteBufferSize:   wsProxyBufferSize,
			EnableCompression: false,
		},
		upgrader: websocket.Upgrader{
			ReadBufferSize:    wsProxyBufferSize,
			WriteBufferSize:   wsProxyBufferSize,
			EnableCompression: false,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}, nil
}

// handleWebSocketProxy は受信した WebSocket を KabuS のローカル WebSocket へ中継する。
func (s *kabuStationWSProxyService) handleWebSocketProxy(c *gin.Context) {
	upstreamHeader := http.Header{}
	if protocols := websocket.Subprotocols(c.Request); len(protocols) > 0 {
		upstreamHeader.Set("Sec-WebSocket-Protocol", strings.Join(protocols, ", "))
	}

	upstreamConn, resp, err := s.dialer.DialContext(c.Request.Context(), s.buildUpstreamURL(c.Request), upstreamHeader)
	if err != nil {
		statusCode := http.StatusBadGateway
		if resp != nil && resp.StatusCode > 0 {
			statusCode = resp.StatusCode
		}

		c.JSON(statusCode, gin.H{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}
	defer upstreamConn.Close()

	clientHeader := http.Header{}
	if subprotocol := upstreamConn.Subprotocol(); subprotocol != "" {
		clientHeader.Set("Sec-WebSocket-Protocol", subprotocol)
	}

	clientConn, err := s.upgrader.Upgrade(c.Writer, c.Request, clientHeader)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}
	defer clientConn.Close()

	errCh := make(chan error, 2)
	go func() {
		errCh <- proxyWebSocketStream(clientConn, upstreamConn)
	}()
	go func() {
		errCh <- proxyWebSocketStream(upstreamConn, clientConn)
	}()

	if err := <-errCh; err != nil && !isExpectedWebSocketClose(err) {
		log.Printf("websocket proxy closed with error: %v", err)
	}
}

// buildUpstreamURL はクエリ文字列を維持したまま中継先 URL を組み立てる。
func (s *kabuStationWSProxyService) buildUpstreamURL(r *http.Request) string {
	target := *s.upstreamURL
	target.RawQuery = r.URL.RawQuery
	return target.String()
}

// proxyWebSocketStream は src から dst へフレームを順方向に転送する。
func proxyWebSocketStream(src *websocket.Conn, dst *websocket.Conn) error {
	buffer := make([]byte, wsProxyBufferSize)

	for {
		messageType, reader, err := src.NextReader()
		if err != nil {
			forwardCloseFrame(dst, err)
			return err
		}

		writer, err := dst.NextWriter(messageType)
		if err != nil {
			return err
		}

		if _, err := io.CopyBuffer(writer, reader, buffer); err != nil {
			_ = writer.Close()
			return err
		}

		if err := writer.Close(); err != nil {
			return err
		}
	}
}

// forwardCloseFrame は受信した Close フレームを対向先へ伝搬する。
func forwardCloseFrame(dst *websocket.Conn, err error) {
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		return
	}

	deadline := time.Now().Add(wsProxyCloseWriteTimeout)
	payload := websocket.FormatCloseMessage(closeErr.Code, closeErr.Text)
	_ = dst.WriteControl(websocket.CloseMessage, payload, deadline)
}

// isExpectedWebSocketClose は正常系の切断かどうかを返す。
func isExpectedWebSocketClose(err error) bool {
	if err == nil {
		return true
	}

	return errors.Is(err, net.ErrClosed) ||
		errors.Is(err, websocket.ErrCloseSent) ||
		websocket.IsCloseError(err,
			websocket.CloseNormalClosure,
			websocket.CloseGoingAway,
			websocket.CloseNoStatusReceived,
		)
}
