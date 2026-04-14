// Package main は kabu ステーション連携用のローカル HTTP コントローラーを提供する。
//
// このパッケージは以下の機能をまとめて扱う。
//
//   - KabuS の起動、ログイン、自動終了
//   - 登録銘柄の永続化と再同期
//   - KabuS WebSocket のプロキシ公開
//   - 先物関連 REST API の定期ポーリングとスナップショット公開
package main
