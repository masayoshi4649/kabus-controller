package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// configPath はアプリケーションが読み込む設定ファイルのパスである。
const configPath = "config.toml"

// defaultKabuStationWebSocketURL は WebSocket 中継先が未設定のときに使う既定値である。
const defaultKabuStationWebSocketURL = "ws://localhost:18080/kabusapi/websocket"

// appConfig は [`config.toml`](config.toml) 全体の設定構造を表す。
type appConfig struct {
	System struct {
		Port int `toml:"Port"`
	} `toml:"SYSTEM"`
	KabuStation struct {
		APIPW        string `toml:"APIPW"`
		Path         string `toml:"PATH"`
		WaitSecond   int    `toml:"WaitSecond"`
		WebSocketURL string `toml:"WebSocketURL"`
	} `toml:"KABUSTATION"`
	Trade struct {
		PollingIntervalMillisec int `toml:"POLLING_INTERVAL_MILLISEC"`
	} `toml:"TRADE"`
}

// loadConfig は [`config.toml`](config.toml) を読み込んでアプリ設定を返す。
func loadConfig() (appConfig, error) {
	var config appConfig

	if _, err := toml.DecodeFile(configPath, &config); err != nil {
		return appConfig{}, err
	}

	return config, nil
}

// loadPort は HTTP サーバーが待ち受けるポート番号を文字列で返す。
func loadPort() (string, error) {
	config, err := loadConfig()
	if err != nil {
		return "", err
	}

	return strconv.Itoa(config.System.Port), nil
}

// loadKabuStationPath は KabuS の実行ファイルパスを返す。
func loadKabuStationPath() (string, error) {
	config, err := loadConfig()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(config.KabuStation.Path), nil
}

// loadKabuStationWebSocketURL は KabuS WebSocket の接続先 URL を返す。
func loadKabuStationWebSocketURL() (string, error) {
	config, err := loadConfig()
	if err != nil {
		return "", err
	}

	wsURL := strings.TrimSpace(config.KabuStation.WebSocketURL)
	if wsURL == "" {
		return defaultKabuStationWebSocketURL, nil
	}

	return wsURL, nil
}

// loadTradePollingInterval は定期ポーリングの実行間隔を返す。
func loadTradePollingInterval() (time.Duration, error) {
	config, err := loadConfig()
	if err != nil {
		return 0, err
	}

	if config.Trade.PollingIntervalMillisec <= 0 {
		return 0, nil
	}

	return time.Duration(config.Trade.PollingIntervalMillisec) * time.Millisecond, nil
}
