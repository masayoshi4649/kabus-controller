package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	kabusapi "github.com/masayoshi4649/kabus-api"
)

const (
	// derivativesProductCode は先物商品を表す product コードである。
	derivativesProductCode = "3"
	// defaultKabuPollingRequestTimeout は 1 回のポーリング要求に対する既定タイムアウトである。
	defaultKabuPollingRequestTimeout = 5 * time.Second
)

var (
	kabuPollingStateMu sync.RWMutex

	// kabuPollingFutureWallet はバックグラウンドポーリングで最後に取得した `/wallet/future` の結果を保持する。
	// 現時点では他 API へ返す前段階のため、グローバル変数として保持し続ける。
	kabuPollingFutureWallet kabusapi.FutureWalletResponse

	// kabuPollingOrders はバックグラウンドポーリングで最後に取得した `product=3` の `/orders` の結果を保持する。
	// 先物注文の最新一覧をプロセス内に一時保持するためのグローバル変数である。
	kabuPollingOrders kabusapi.OrdersResponse

	// kabuPollingPositions はバックグラウンドポーリングで最後に取得した `product=3` の `/positions` の結果を保持する。
	// 先物建玉の最新一覧をプロセス内に一時保持するためのグローバル変数である。
	kabuPollingPositions kabusapi.PositionsResponse

	// kabuPollingLastUpdatedAt は上記ポーリング結果のいずれかを最後に更新した時刻を保持する。
	kabuPollingLastUpdatedAt time.Time

	kabuPollingServiceMu     sync.RWMutex
	activeKabuPollingService *kabuPollingService
)

// kabuPollingService は KabuS REST API の定期ポーリング実行を管理する。
//
// interval はポーリング周期、ctx は停止制御、inFlight は多重実行防止に使う。
// 前回の周期処理が完了していない間は次の周期を起動しないことで、
// 外部 API の遅延時にも goroutine が滞留し続けないようにする。
type kabuPollingService struct {
	interval time.Duration
	ctx      context.Context
	inFlight chan struct{}
}

// newKabuPollingService は設定値から KabuS 定期ポーリングサービスを生成する。
func newKabuPollingService() (*kabuPollingService, error) {
	interval, err := loadTradePollingInterval()
	if err != nil {
		return nil, err
	}

	return &kabuPollingService{
		interval: interval,
		inFlight: make(chan struct{}, 1),
	}, nil
}

// Start は KabuS 定期ポーリングをバックグラウンドで開始する。
func (s *kabuPollingService) Start(ctx context.Context) {
	if s == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.ctx = ctx
	setActiveKabuPollingService(s)

	if s.interval <= 0 {
		log.Printf("TRADE.POLLING_INTERVAL_MILLISEC が 0 以下のため、KabuS ポーリングは開始しません")
		return
	}

	s.trigger()

	go func() {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.trigger()
			}
		}
	}()
}

// triggerKabuPollingNow はログイン直後などに定期ポーリングを即時実行する。
func triggerKabuPollingNow() {
	kabuPollingServiceMu.RLock()
	service := activeKabuPollingService
	kabuPollingServiceMu.RUnlock()

	if service == nil {
		return
	}

	service.trigger()
}

// clearKabuPollingState は保持中のポーリング結果を初期化する。
func clearKabuPollingState() {
	kabuPollingStateMu.Lock()
	defer kabuPollingStateMu.Unlock()

	kabuPollingFutureWallet = kabusapi.FutureWalletResponse{}
	kabuPollingOrders = nil
	kabuPollingPositions = nil
	kabuPollingLastUpdatedAt = time.Time{}
}

// currentKabuPollingFutureWallet は保持中の先物余力スナップショットを返す。
func currentKabuPollingFutureWallet() kabusapi.FutureWalletResponse {
	kabuPollingStateMu.RLock()
	defer kabuPollingStateMu.RUnlock()

	return kabuPollingFutureWallet
}

// currentKabuPollingOrders は保持中の先物注文一覧スナップショットを返す。
func currentKabuPollingOrders() kabusapi.OrdersResponse {
	kabuPollingStateMu.RLock()
	defer kabuPollingStateMu.RUnlock()

	if kabuPollingOrders == nil {
		return kabusapi.OrdersResponse{}
	}

	return append(kabusapi.OrdersResponse(nil), kabuPollingOrders...)
}

// currentKabuPollingPositions は保持中の先物建玉一覧スナップショットを返す。
func currentKabuPollingPositions() kabusapi.PositionsResponse {
	kabuPollingStateMu.RLock()
	defer kabuPollingStateMu.RUnlock()

	if kabuPollingPositions == nil {
		return kabusapi.PositionsResponse{}
	}

	return append(kabusapi.PositionsResponse(nil), kabuPollingPositions...)
}

// setActiveKabuPollingService は現在有効なポーリングサービスを差し替える。
func setActiveKabuPollingService(service *kabuPollingService) {
	kabuPollingServiceMu.Lock()
	defer kabuPollingServiceMu.Unlock()

	activeKabuPollingService = service
}

// trigger はポーリング 1 回分の非同期実行を要求する。
// 既に実行中のポーリングがある場合は何もせず終了する。
func (s *kabuPollingService) trigger() {
	if s == nil || s.ctx == nil {
		return
	}

	select {
	case s.inFlight <- struct{}{}:
		go func() {
			defer func() {
				<-s.inFlight
			}()

			ctx, cancel := context.WithTimeout(s.ctx, defaultKabuPollingRequestTimeout)
			defer cancel()

			if err := s.pollOnce(ctx); err != nil && ctx.Err() == nil {
				log.Printf("KabuS ポーリングに失敗しました: %v", err)
			}
		}()
	default:
	}
}

// pollOnce は KabuS REST API から 1 周期分のスナップショットを取得する。
//
// `/wallet/future`、`/orders?product=3`、`/positions?product=3` はすべて並列取得し、
// 取得できた内容をグローバルスナップショットへ反映する。
func (s *kabuPollingService) pollOnce(ctx context.Context) error {
	sessionState := currentKabuStationSessionState()
	if sessionState.PID <= 0 {
		return nil
	}

	token := strings.TrimSpace(sessionState.APIKey)
	if token == "" {
		return nil
	}

	client := kabusapi.NewClient(kabusapi.Config{Token: token})
	defer client.CloseIdleConnections()

	var wg sync.WaitGroup
	errCh := make(chan error, 3)

	wg.Add(3)

	go func() {
		defer wg.Done()

		response, err := client.GetFutureWallet(ctx)
		if err != nil {
			errCh <- fmt.Errorf("/wallet/future: %w", err)
			return
		}

		storeKabuPollingFutureWallet(response)
	}()

	go func() {
		defer wg.Done()

		orderDetails := false
		response, err := client.ListOrders(ctx, kabusapi.OrdersOptions{
			Product: derivativesProductCode,
			Details: &orderDetails,
		})
		if err != nil {
			errCh <- fmt.Errorf("/orders?product=%s: %w", derivativesProductCode, err)
			return
		}

		storeKabuPollingOrders(response)
	}()

	go func() {
		defer wg.Done()

		response, err := client.ListPositions(ctx, kabusapi.PositionsOptions{Product: derivativesProductCode})
		if err != nil {
			errCh <- fmt.Errorf("/positions?product=%s: %w", derivativesProductCode, err)
			return
		}

		storeKabuPollingPositions(response)
	}()

	wg.Wait()
	close(errCh)

	var messages []string
	for err := range errCh {
		messages = append(messages, err.Error())
	}

	if len(messages) > 0 {
		return fmt.Errorf("%s", strings.Join(messages, "; "))
	}

	return nil
}

// storeKabuPollingFutureWallet は先物余力スナップショットを保存する。
func storeKabuPollingFutureWallet(response kabusapi.FutureWalletResponse) {
	kabuPollingStateMu.Lock()
	defer kabuPollingStateMu.Unlock()

	kabuPollingFutureWallet = response
	kabuPollingLastUpdatedAt = time.Now()
	// debugLogKabuPollingUpdate("/wallet/future updated")
}

// storeKabuPollingOrders は先物注文一覧スナップショットを保存する。
func storeKabuPollingOrders(response kabusapi.OrdersResponse) {
	cloned := append(kabusapi.OrdersResponse(nil), response...)

	kabuPollingStateMu.Lock()
	defer kabuPollingStateMu.Unlock()

	kabuPollingOrders = cloned
	kabuPollingLastUpdatedAt = time.Now()
	// debugLogKabuPollingUpdate(fmt.Sprintf("/orders?product=%s updated count=%d", derivativesProductCode, len(cloned)))
}

// storeKabuPollingPositions は先物建玉一覧スナップショットを保存する。
func storeKabuPollingPositions(response kabusapi.PositionsResponse) {
	cloned := append(kabusapi.PositionsResponse(nil), response...)

	kabuPollingStateMu.Lock()
	defer kabuPollingStateMu.Unlock()

	kabuPollingPositions = cloned
	kabuPollingLastUpdatedAt = time.Now()
	// debugLogKabuPollingUpdate(fmt.Sprintf("/positions?product=%s updated count=%d", derivativesProductCode, len(cloned)))
}

// debugLogKabuPollingUpdate は DEBUG 用にポーリング更新内容を標準ログへ出力する。
func debugLogKabuPollingUpdate(message string) {
	log.Println(message)
}
