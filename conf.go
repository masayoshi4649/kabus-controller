package main

import (
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

const configPath = "config.toml"
const defaultKabuStationWebSocketURL = "ws://localhost:18080/kabusapi/websocket"

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
