package main

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	kabusapi "github.com/masayoshi4649/kabus-api"
	bbolt "go.etcd.io/bbolt"
)

const (
	// defaultKabuLoginTimeout は KabuS ログイン自動化の既定タイムアウトである。
	defaultKabuLoginTimeout = 60 * time.Second
	// defaultPowerShellPath は UI Automation 実行に使う PowerShell の既定コマンドである。
	defaultPowerShellPath = "powershell.exe"
	// defaultSymbolStorePath は登録銘柄一覧を保存する bbolt ファイルの既定パスである。
	defaultSymbolStorePath = "db/kabus_symbols.db"
	// symbolStoreBucketName は登録銘柄一覧を格納する bbolt バケット名である。
	symbolStoreBucketName = "registered_symbols"
)

var (
	kabuStationStateMu          sync.RWMutex
	kabuStationPID              int
	kabuStationAPIKey           string
	kabuStationSessionStartedAt time.Time
)

// kabuStationSessionState は現在保持している KabuS セッション情報を表す。
type kabuStationSessionState struct {
	PID       int
	APIKey    string
	StartedAt time.Time
}

// kabuLoginService は KabuS の起動、ログイン、終了、および銘柄同期を制御する。
type kabuLoginService struct {
	exePath        string
	timeout        time.Duration
	clickWait      time.Duration
	apiPassword    string
	powerShellPath string
	symbolStore    *symbolStore
	mu             sync.Mutex
}

// processInfo は `tasklist.exe` から取得したプロセス情報を表す。
type processInfo struct {
	ImageName string
	PID       int
}

// symbolStore は登録銘柄一覧を bbolt へ永続化するためのストアである。
type symbolStore struct {
	path string
}

// storedRegisterSymbol は KabuS 登録銘柄の永続化形式を表す。
type storedRegisterSymbol struct {
	Symbol   string `json:"symbol"`
	Exchange int    `json:"exchange"`
}

// currentKabuStationSessionState は現在保持している KabuS セッション状態を返す。
func currentKabuStationSessionState() kabuStationSessionState {
	kabuStationStateMu.RLock()
	defer kabuStationStateMu.RUnlock()

	return kabuStationSessionState{
		PID:       kabuStationPID,
		APIKey:    kabuStationAPIKey,
		StartedAt: kabuStationSessionStartedAt,
	}
}

// storeKabuStationPID は保持する KabuS PID とセッション開始時刻を更新する。
func storeKabuStationPID(pid int, startedAt time.Time) {
	kabuStationStateMu.Lock()
	defer kabuStationStateMu.Unlock()

	kabuStationPID = pid
	kabuStationSessionStartedAt = startedAt
}

// storeKabuStationAPIKey は保持する APIKey を更新する。
func storeKabuStationAPIKey(apiKey string) {
	kabuStationStateMu.Lock()
	kabuStationAPIKey = strings.TrimSpace(apiKey)
	kabuStationStateMu.Unlock()

	triggerKabuPollingNow()
}

// clearKabuStationSessionState は保持している KabuS セッション状態を初期化する。
func clearKabuStationSessionState() {
	kabuStationStateMu.Lock()
	kabuStationPID = 0
	kabuStationAPIKey = ""
	kabuStationSessionStartedAt = time.Time{}
	kabuStationStateMu.Unlock()

	clearKabuPollingState()
}

// newKabuLoginService は設定ファイルを元に KabuS 制御サービスを生成する。
func newKabuLoginService() (*kabuLoginService, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, err
	}

	if config.KabuStation.WaitSecond < 0 {
		return nil, fmt.Errorf("KABUSTATION.WaitSecond には 0 以上を指定してください: %d", config.KabuStation.WaitSecond)
	}

	exePath := strings.TrimSpace(config.KabuStation.Path)
	if exePath == "" {
		return nil, fmt.Errorf("KABUSTATION.PATH を config.toml に設定してください")
	}

	return &kabuLoginService{
		exePath:        exePath,
		timeout:        defaultKabuLoginTimeout,
		clickWait:      time.Duration(config.KabuStation.WaitSecond) * time.Second,
		apiPassword:    strings.TrimSpace(config.KabuStation.APIPW),
		powerShellPath: defaultPowerShellPath,
		symbolStore:    newSymbolStore(defaultSymbolStorePath),
	}, nil
}

// newSymbolStore は登録銘柄を保持する bbolt ストアを生成する。
func newSymbolStore(path string) *symbolStore {
	return &symbolStore{path: path}
}

// LoadStoredSymbols は bbolt に保持された登録銘柄一覧を返す。
func (s *kabuLoginService) LoadStoredSymbols() ([]storedRegisterSymbol, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.symbolStore.LoadSymbols()
}

// ReplaceStoredSymbols は bbolt に保持する登録銘柄一覧を置き換える。
func (s *kabuLoginService) ReplaceStoredSymbols(symbols []storedRegisterSymbol) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.symbolStore.ReplaceSymbols(symbols)
}

// TriggerLogin は KabuS を起動してログイン操作、APIKey 取得、銘柄再登録を実行する。
func (s *kabuLoginService) TriggerLogin() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if runtime.GOOS != "windows" {
		return fmt.Errorf("KabuS のログイン操作は Windows でのみ利用できます")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second+s.timeout+15*time.Second)
	defer cancel()

	pid, err := clickKabuStationLogin(ctx, s.exePath, s.timeout, s.clickWait, s.powerShellPath)
	if pid > 0 {
		storeKabuStationPID(pid, time.Now())
	}
	if err != nil {
		return err
	}

	tokenCtx, cancel := context.WithTimeout(context.Background(), s.timeout+30*time.Second)
	defer cancel()

	apiClient := kabusapi.NewClient(kabusapi.Config{})
	defer apiClient.CloseIdleConnections()

	if err := issueAndPrintKabuAPIKey(tokenCtx, apiClient, s.apiPassword); err != nil {
		return err
	}

	registerCtx, cancel := context.WithTimeout(context.Background(), s.timeout+30*time.Second)
	defer cancel()

	return s.syncStoredSymbols(registerCtx, apiClient)
}

// SyncStoredSymbols は保持済み APIKey を使って DB 登録銘柄の全解除・再登録を実行する。
func (s *kabuLoginService) SyncStoredSymbols(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if runtime.GOOS != "windows" {
		return fmt.Errorf("KabuS の銘柄同期は Windows でのみ利用できます")
	}

	apiClient, err := s.newAuthenticatedAPIClient(ctx)
	if err != nil {
		return err
	}
	defer apiClient.CloseIdleConnections()

	return s.syncStoredSymbols(ctx, apiClient)
}

// syncStoredSymbols は指定クライアントを使って DB 登録銘柄の全解除・再登録を実行する。
func (s *kabuLoginService) syncStoredSymbols(ctx context.Context, client *kabusapi.Client) error {
	return registerDefinedSymbols(ctx, client, s.symbolStore)
}

// newAuthenticatedAPIClient は現在の APIKey または APIPW を使って認証済みクライアントを返す。
func (s *kabuLoginService) newAuthenticatedAPIClient(ctx context.Context) (*kabusapi.Client, error) {
	sessionState := currentKabuStationSessionState()
	apiClient := kabusapi.NewClient(kabusapi.Config{Token: strings.TrimSpace(sessionState.APIKey)})
	if strings.TrimSpace(apiClient.Token()) != "" {
		return apiClient, nil
	}

	if err := issueAndPrintKabuAPIKey(ctx, apiClient, s.apiPassword); err != nil {
		apiClient.CloseIdleConnections()
		return nil, err
	}

	return apiClient, nil
}

// Exit は保持済み PID を使って KabuS を終了し内部状態を初期化する。
func (s *kabuLoginService) Exit() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if runtime.GOOS != "windows" {
		return fmt.Errorf("KabuS の終了操作は Windows でのみ利用できます")
	}

	sessionState := currentKabuStationSessionState()
	if sessionState.PID <= 0 {
		return fmt.Errorf("保持している KabuS の PID がありません。先に /kabus/login を実行してください")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := exitKabuStation(ctx, sessionState.PID); err != nil {
		return err
	}

	clearKabuStationSessionState()

	return nil
}

// clickKabuStationLogin は KabuS の起動確認後にログインボタン押下を実行し PID を返す。
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
//	defer cancel()
//
//	pid, err := clickKabuStationLogin(
//		ctx,
//		`C:\Users\example\AppData\Local\kabuStation\KabuS.exe`,
//		60*time.Second,
//		5*time.Second,
//		"powershell.exe",
//	)
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println(pid)
func clickKabuStationLogin(ctx context.Context, exePath string, timeout time.Duration, clickWait time.Duration, powerShellPath string) (int, error) {
	proc, found, err := findProcessByImageName(ctx, "KabuS.exe")
	if err != nil {
		return 0, fmt.Errorf("KabuS.exe の起動確認に失敗しました: %w", err)
	}

	pid := proc.PID
	if !found {
		fmt.Printf("KabuS.exe を起動します: %s\n", exePath)
		pid, err = startProcess(exePath)
		if err != nil {
			return 0, fmt.Errorf("KabuS.exe の起動に失敗しました: %w", err)
		}

		time.Sleep(500 * time.Millisecond)
	} else {
		fmt.Printf("KabuS.exe は既に起動しています。PID: %d\n", pid)
	}

	if err := runLoginAutomation(ctx, timeout, clickWait, powerShellPath); err != nil {
		return pid, err
	}

	return pid, nil
}

// findProcessByImageName は イメージ名に一致するプロセス情報を取得する。
func findProcessByImageName(ctx context.Context, imageName string) (processInfo, bool, error) {
	cmd := exec.CommandContext(ctx, "tasklist.exe", "/FI", fmt.Sprintf("IMAGENAME eq %s", imageName), "/FO", "CSV", "/NH")
	out, err := cmd.Output()
	if err != nil {
		return processInfo{}, false, err
	}

	return parseTasklistOutput(out)
}

// findProcessByPID は PID に一致するプロセス情報を取得する。
func findProcessByPID(ctx context.Context, pid int) (processInfo, bool, error) {
	cmd := exec.CommandContext(ctx, "tasklist.exe", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
	out, err := cmd.Output()
	if err != nil {
		return processInfo{}, false, err
	}

	return parseTasklistOutput(out)
}

// parseTasklistOutput は `tasklist.exe` の CSV 出力を解析して先頭プロセス情報を返す。
func parseTasklistOutput(out []byte) (processInfo, bool, error) {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return processInfo{}, false, nil
	}

	firstLine := text
	if idx := strings.IndexAny(firstLine, "\r\n"); idx >= 0 {
		firstLine = firstLine[:idx]
	}

	if !strings.HasPrefix(strings.TrimSpace(firstLine), `"`) {
		return processInfo{}, false, nil
	}

	records, err := csv.NewReader(strings.NewReader(text)).ReadAll()
	if err != nil {
		return processInfo{}, false, fmt.Errorf("tasklist 出力の解析に失敗しました: %w", err)
	}

	for _, record := range records {
		if len(record) < 2 {
			continue
		}

		pid, err := strconv.Atoi(strings.TrimSpace(record[1]))
		if err != nil {
			return processInfo{}, false, fmt.Errorf("PID の解析に失敗しました: %w", err)
		}

		return processInfo{
			ImageName: strings.TrimSpace(record[0]),
			PID:       pid,
		}, true, nil
	}

	return processInfo{}, false, nil
}

// startProcess は指定された実行ファイルを起動し PID を返す。
func startProcess(exePath string) (int, error) {
	if _, err := os.Stat(exePath); err != nil {
		return 0, err
	}

	cmd := exec.Command(exePath)
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return 0, err
	}

	return pid, nil
}

// exitKabuStation は PID に対応する KabuS.exe を強制終了する。
func exitKabuStation(ctx context.Context, pid int) error {
	proc, found, err := findProcessByPID(ctx, pid)
	if err != nil {
		return fmt.Errorf("PID %d の確認に失敗しました: %w", pid, err)
	}

	if !found {
		return fmt.Errorf("PID %d のプロセスが見つかりませんでした", pid)
	}

	if !strings.EqualFold(proc.ImageName, "KabuS.exe") {
		return fmt.Errorf("PID %d は KabuS.exe ではありません: %s", pid, proc.ImageName)
	}

	cmd := exec.CommandContext(ctx, "taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("KabuS.exe の終了に失敗しました: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

// registerDefinedSymbols は既存の登録銘柄を全解除し DB 保持銘柄を再登録する。
func registerDefinedSymbols(ctx context.Context, client *kabusapi.Client, store *symbolStore) error {
	if client == nil {
		err := fmt.Errorf("kabusapi client is nil")
		fmt.Printf("register error: %v\n", err)
		return err
	}

	if _, err := client.UnregisterAllSymbols(ctx); err != nil {
		wrapped := fmt.Errorf("銘柄登録全解除に失敗しました: %w", err)
		fmt.Printf("register error: %v\n", wrapped)
		return wrapped
	}

	request, err := store.LoadRegisterRequest()
	if err != nil {
		fmt.Printf("register error: %v\n", err)
		return err
	}

	if len(request.Symbols) == 0 {
		fmt.Printf("register skip: no symbols\n")
		return nil
	}

	fmt.Printf("register symbols: %+v\n", request.Symbols)

	if _, err := client.RegisterSymbols(ctx, request); err != nil {
		wrapped := fmt.Errorf("銘柄登録に失敗しました: %w", err)
		fmt.Printf("register error: %v\n", wrapped)
		return wrapped
	}

	fmt.Printf("register success: %d symbols\n", len(request.Symbols))

	return nil
}

// openDB は symbol store 用の bbolt DB を開く。
func (s *symbolStore) openDB() (*bbolt.DB, error) {
	if s == nil {
		return nil, fmt.Errorf("symbol store is nil")
	}

	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("symbol store ディレクトリの作成に失敗しました: %w", err)
		}
	}

	db, err := bbolt.Open(s.path, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("symbol store のオープンに失敗しました: %w", err)
	}

	return db, nil
}

// LoadSymbols は bbolt に保持した登録銘柄一覧を返す。
func (s *symbolStore) LoadSymbols() ([]storedRegisterSymbol, error) {
	db, err := s.openDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(symbolStoreBucketName))
		return err
	}); err != nil {
		return nil, fmt.Errorf("symbol store バケット初期化に失敗しました: %w", err)
	}

	symbols := make([]storedRegisterSymbol, 0)
	if err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(symbolStoreBucketName))
		if bucket == nil {
			return fmt.Errorf("symbol store バケットが存在しません")
		}

		return bucket.ForEach(func(_, value []byte) error {
			var record storedRegisterSymbol
			if err := json.Unmarshal(value, &record); err != nil {
				return fmt.Errorf("登録銘柄データの解析に失敗しました: %w", err)
			}

			symbol := strings.TrimSpace(record.Symbol)
			if symbol == "" {
				return fmt.Errorf("登録銘柄データに Symbol がありません")
			}

			if record.Exchange <= 0 {
				return fmt.Errorf("登録銘柄データに不正な Exchange があります: %d", record.Exchange)
			}

			record.Symbol = symbol
			symbols = append(symbols, record)

			return nil
		})
	}); err != nil {
		return nil, err
	}

	return symbols, nil
}

// LoadRegisterRequest は bbolt に保持した銘柄一覧から登録リクエストを構築する。
func (s *symbolStore) LoadRegisterRequest() (kabusapi.RegisterRequest, error) {
	records, err := s.LoadSymbols()
	if err != nil {
		return kabusapi.RegisterRequest{}, err
	}

	symbols := make([]kabusapi.RegisterSymbol, 0, len(records))
	for _, record := range records {
		symbols = append(symbols, kabusapi.RegisterSymbol{
			Symbol:   record.Symbol,
			Exchange: record.Exchange,
		})
	}

	return kabusapi.RegisterRequest{Symbols: symbols}, nil
}

// ReplaceSymbols は bbolt に保持する登録銘柄一覧を全件置き換える。
func (s *symbolStore) ReplaceSymbols(symbols []storedRegisterSymbol) error {
	db, err := s.openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	normalized := make([]storedRegisterSymbol, 0, len(symbols))
	for i, symbol := range symbols {
		symbol.Symbol = strings.TrimSpace(symbol.Symbol)
		if symbol.Symbol == "" {
			return fmt.Errorf("登録銘柄[%d] の Symbol が空です", i)
		}

		if symbol.Exchange <= 0 {
			return fmt.Errorf("登録銘柄[%d] の Exchange が不正です: %d", i, symbol.Exchange)
		}

		normalized = append(normalized, symbol)
	}

	if err := db.Update(func(tx *bbolt.Tx) error {
		if err := tx.DeleteBucket([]byte(symbolStoreBucketName)); err != nil && err != bbolt.ErrBucketNotFound {
			return fmt.Errorf("既存バケット削除に失敗しました: %w", err)
		}

		bucket, err := tx.CreateBucket([]byte(symbolStoreBucketName))
		if err != nil {
			return fmt.Errorf("バケット作成に失敗しました: %w", err)
		}

		for i, symbol := range normalized {
			payload, err := json.Marshal(symbol)
			if err != nil {
				return fmt.Errorf("登録銘柄のシリアライズに失敗しました: %w", err)
			}

			key := []byte(strconv.Itoa(i))
			if err := bucket.Put(key, payload); err != nil {
				return fmt.Errorf("登録銘柄の保存に失敗しました: %w", err)
			}
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

// issueAndPrintKabuAPIKey は API パスワードでトークンを取得し標準出力と内部状態へ保存する。
func issueAndPrintKabuAPIKey(ctx context.Context, client *kabusapi.Client, apiPassword string) error {
	apiPassword = strings.TrimSpace(apiPassword)
	if apiPassword == "" {
		return fmt.Errorf("KABUSTATION.APIPW が設定されていません")
	}

	if client == nil {
		return fmt.Errorf("kabusapi client is nil")
	}

	var lastErr error
	for {
		response, err := client.IssueToken(ctx, kabusapi.TokenRequest{APIPassword: apiPassword})
		if err == nil {
			token := strings.TrimSpace(response.Token)
			if token != "" {
				storeKabuStationAPIKey(token)
				fmt.Printf("APIKey: %s\n", token)
				return nil
			}

			lastErr = fmt.Errorf("APIKey が空で返されました")
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("APIKey の取得に失敗しました: %w", lastErr)
			}

			return fmt.Errorf("APIKey の取得がタイムアウトしました: %w", ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}
}

// runLoginAutomation は PowerShell UI Automation を起動してログインボタン押下を実行する。
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
//	defer cancel()
//
//	err := runLoginAutomation(ctx, 60*time.Second, 5*time.Second, "powershell.exe")
//	if err != nil {
//		log.Fatal(err)
//	}
func runLoginAutomation(ctx context.Context, timeout time.Duration, clickWait time.Duration, powerShellPath string) error {
	script := loginAutomationScript(int(timeout/time.Second), int(clickWait/time.Second))
	encoded := encodePowerShellScript(script)

	cmd := exec.CommandContext(
		ctx,
		powerShellPath,
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-EncodedCommand",
		encoded,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ログイン UI 操作に失敗しました: %w", err)
	}

	return nil
}

// encodePowerShellScript は PowerShell の `-EncodedCommand` 用に UTF-16LE の Base64 文字列へ変換する。
func encodePowerShellScript(script string) string {
	encoded := utf16.Encode([]rune(script))
	buf := make([]byte, len(encoded)*2)

	for i, r := range encoded {
		buf[i*2] = byte(r)
		buf[i*2+1] = byte(r >> 8)
	}

	return base64.StdEncoding.EncodeToString(buf)
}

// loginAutomationScript は KabuS のログインボタンを押下する PowerShell スクリプトを生成する。
func loginAutomationScript(timeoutSeconds int, clickWaitSeconds int) string {
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes

function Get-KabuSMainWindow {
    param([int]$TimeoutSeconds = 30)

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $proc = Get-Process -Name 'KabuS' -ErrorAction SilentlyContinue
        if ($proc -and $proc.MainWindowHandle -ne 0) {
            return [System.Windows.Automation.AutomationElement]::FromHandle($proc.MainWindowHandle)
        }
        Start-Sleep -Milliseconds 500
    }

    throw 'タイムアウト: KabuS のメインウィンドウが取得できませんでした。'
}

function Wait-And-FindLoginButton {
    param(
        [System.Windows.Automation.AutomationElement]$WindowElement,
        [int]$TimeoutSeconds = 60
    )

    $ctrlTypeProp = [System.Windows.Automation.AutomationElement]::ControlTypeProperty
    $nameProp = [System.Windows.Automation.AutomationElement]::NameProperty
    $frameworkProp = [System.Windows.Automation.AutomationElement]::FrameworkIdProperty

    $condButton = New-Object System.Windows.Automation.PropertyCondition(
        $ctrlTypeProp,
        [System.Windows.Automation.ControlType]::Button
    )
    $condName = New-Object System.Windows.Automation.PropertyCondition(
        $nameProp,
        'ログイン'
    )
    $condFramework = New-Object System.Windows.Automation.PropertyCondition(
        $frameworkProp,
        'Chrome'
    )

    $andCond = New-Object System.Windows.Automation.AndCondition(
        @($condButton, $condName, $condFramework)
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        Write-Host 'ログインボタンを探索しています...'

        $btn = $WindowElement.FindFirst(
            [System.Windows.Automation.TreeScope]::Descendants,
            $andCond
        )

        if ($btn) {
            return $btn
        }

        Start-Sleep -Milliseconds 500
    }

    throw 'タイムアウト: ログインボタン (Name=''ログイン'', Button, FrameworkId=''Chrome'') が見つかりませんでした。'
}

function Invoke-Element {
    param([System.Windows.Automation.AutomationElement]$Element)

    $invokePattern = $Element.GetCurrentPattern(
        [System.Windows.Automation.InvokePattern]::Pattern
    )
    $invokePattern.Invoke()
}

$timeoutSeconds = %d
$clickWaitSeconds = %d

Write-Host 'KabuS のメインウィンドウを待っています...'
$mainWin = Get-KabuSMainWindow -TimeoutSeconds 30
Write-Host 'メインウィンドウ取得:' $mainWin.Current.Name

$loginButton = Wait-And-FindLoginButton -WindowElement $mainWin -TimeoutSeconds $timeoutSeconds

if ($clickWaitSeconds -gt 0) {
    Write-Host ('ログインボタン押下前に待機しています... ' + $clickWaitSeconds + ' 秒')
    Start-Sleep -Seconds $clickWaitSeconds
}

Write-Host 'ログインボタン発見。Invoke 実行...'
Invoke-Element -Element $loginButton
Write-Host 'ログインボタンを Invoke しました。外部認証が別画面で続く場合は、そのまま手動で完了してください。'
`, timeoutSeconds, clickWaitSeconds)
}
