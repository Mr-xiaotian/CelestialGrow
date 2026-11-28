# pkg/assembly/assembly_test.go

> 📅 最終更新日: 2026/11/26

本ファイルは `pkg/assembly` のエンドツーエンド組み立てテストで、外部テストパッケージ `assembly_test` を採用しています（公開 API のみを通じてアクセスし、非公開フィールドには触れません）。テストはローカル組み立て、リモート報告の組み立て、パラメータ検証の 3 種類の経路をカバーし、合計 5 つのテスト関数と 2 つの補助定義からなります。

## テスト対象

| テスト | カバーする能力 | 重要な不変条件 |
|------|----------|-----------|
| `TestStartLocalResourcesAssembleLifecycle` | ローカル組み立て + イベントの永続化 + 重複 Stop + 実行後の照会 | 非 nil の `StopFunc` を返す；`SeedInputEvent` が Hub 経由で SQLite に書き込まれる；連続 2 回の `stop()` がいずれも `nil` を返す；`scarecrow.Find` で `*persist.LifecycleWatcher` を取得できる；`DBPath()` が非空；スナップショットの状態が `seed`、`SeedJSON` が `"42"` |
| `TestStartReportResourcesAssembleReporter` | リモート組み立て + プッシュ経路 | `*reporter.PushWatcher` が watcher Hub に登録される；`*reporter.SnapshotTicker` と `*reporter.InjectionTicker` が ticker Hub に登録される；`FarmStartEvent` が最終的に `POST /api/push_graph_meta` をトリガーする |
| `TestStartLocalResourcesRejectNilHub` | ローカルエントリの watcher Hub 境界 | `watcherHub == nil` のとき非 nil の error を返す |
| `TestStartLocalResourcesRejectNilTickerHub` | ローカルエントリの ticker Hub 境界 | `tickerHub == nil` のとき非 nil の error を返す |
| `TestStartReportResourcesRejectInvalidArguments` | リモートエントリのパラメータ境界 | `report == nil`、`injectionTarget == nil` がそれぞれ非 nil の error を返す |

テストの中核となる経路は：**2 つの Hub を作成 → リソースを組み立て → Hub にイベントを送信 → Stop → Hub からオブザーバーを取得 → データベースパスで状態スナップショットを読み戻す / HTTP リクエストの到達を待つ**。

## 主要なテスト詳細

### 補助定義

```go
func waitForPath(t *testing.T, paths <-chan string, want string) {
    t.Helper()
    timeout := time.After(3 * time.Second)
    for {
        select {
        case got := <-paths:
            if got == want {
                return
            }
        case <-timeout:
            t.Fatalf("did not receive %s", want)
        }
    }
}

type injectionTargetStub struct{}

func (injectionTargetStub) InjectTasks(map[string][]any) error { return nil }
func (injectionTargetStub) InjectTerminations([]string) error  { return nil }
```

- `waitForPath` は非同期プッシュに最大 3 秒の猶予を与え、一致しないパスは無視します。
- `injectionTargetStub` は `reporter.InjectionTarget` の 2 つのメソッドを実装し、リモートテストが実際の `*farm.Farm` に依存しないようにします。

### `TestStartLocalResourcesAssembleLifecycle`

```go
hub := scarecrow.NewHub(scarecrow.WithErrorHandler(func(err error) {
    t.Errorf("unexpected watcher error: %v", err)
}))
tickerHub, err := metronome.NewHub(metronome.WithErrorHandler(func(err error) {
    t.Errorf("unexpected ticker error: %v", err)
}))
if err != nil {
    t.Fatalf("NewHub() = %v", err)
}

stop, err := assembly.StartLocalResources(hub, tickerHub, "DEBUG", metronome.DefaultInterval)
if err != nil {
    t.Fatalf("StartLocalResources() = %v", err)
}

now := time.Now()
if err := hub.OnEvent(scarecrow.SeedInputEvent{
    Plot: "node", Seed: 42, SeedRepr: "42", SeedID: 1, Time: now,
}); err != nil {
    t.Fatalf("OnEvent() = %v", err)
}

if err := stop(); err != nil {
    t.Fatalf("stop() = %v", err)
}
if err := stop(); err != nil {
    t.Fatalf("second stop() = %v", err)
}
```

要点：

1. **両方の Hub のエラーハンドラが直接テスト失敗として記録する**：`Hub.OnEvent` はオブザーバーの障害を `ErrorHandler` に渡すだけで常に `nil` を返すため、ハンドラを介して「オブザーバーがイベント処理時にエラーを出した」ことを捕捉する必要があります。
2. **ログレベルに `DEBUG` を渡す**：`SeedInputEvent` は `LogWatcher` で `DEBUG` レベルのログにマッピングされます。`DEBUG` を渡せばこのイベントが最低レベルでフィルタされません（本テストはログファイルの内容を検証しません）。
3. **`StopFunc` を連続 2 回呼び、いずれも `nil` を要求する**：`stopped` の冪等保護を検証します。下位の `funnel.Spout.Stop` はキューチャネルを `close` するため、単純に重複呼び出しすると `close of closed channel` で panic します。したがってこの 2 行は冪等実装を守っています。
4. **先に Stop してから照会**：`Stop` は消費コルーチンがキューを処理し終えて SQLite を閉じるまで待ちます。その後 `LoadLifecycleStatusesFromPath` がパスでデータベースファイルを開き直して読み取るため、フラッシュ前のデータを読むことを避けられます。

照会とアサーション：

```go
lifecycle, ok := scarecrow.Find[*persist.LifecycleWatcher](hub)
if !ok {
    t.Fatal("lifecycle watcher is not registered")
}
dbPath := lifecycle.DBPath()
if dbPath == "" {
    t.Fatal("lifecycle database path is empty")
}

records, err := persist.LoadLifecycleStatusesFromPath(dbPath, "node")
if err != nil {
    t.Fatalf("LoadLifecycleStatusesFromPath() = %v", err)
}
if len(records) != 1 {
    t.Fatalf("records = %d, want 1", len(records))
}
if records[0].Status != "seed" || records[0].SeedJSON != "42" {
    t.Fatalf("record = %#v, want seed/42", records[0])
}
```

- `scarecrow.Find` は Hub およびそのネストした Hub の中で最初の `*persist.LifecycleWatcher` を探し、オブザーバーが確かに**渡された hub** に登録されたことを検証します。
- `DBPath()` が非空であることは、`LifecycleWatcher.Start` が `BeforeStart` まで実行されて SQLite ファイルを作成したことを示します。`Find` の成功と合わせて、このオブザーバーが起動済みかつ登録済みであることを確認できます。
- `len(records) == 1` は、その plot に入力イベント 1 件に対応する状態スナップショットが 1 つだけあることを検証します。
- `Status == "seed"` となるのは `SeedInputEvent` のみを送信し、後続の ripen / wither / prune イベントがないためです。`SeedJSON == "42"` は `persist` による `Seed` フィールドの JSON シリアライズ（`int` 42 → `42`）に由来します。

### `TestStartReportResourcesAssembleReporter`

テストはまず `httptest.NewServer` でリクエストパスを記録するローカルサービスを起動します：

```go
paths := make(chan string, 16)
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    select {
    case paths <- r.URL.Path:
    default:
    }
    w.WriteHeader(http.StatusOK)
}))
defer server.Close()
```

組み立て時に `server.URL` とセッション識別子を `reporter.Config` に渡します：

```go
stop, err := assembly.StartReportResources(
    hub,
    tickerHub,
    injectionTargetStub{},
    "INFO",
    metronome.DefaultInterval,
    &reporter.Config{URL: server.URL, SessionID: "farm@1"},
)
if err != nil {
    t.Fatalf("StartReportResources() = %v", err)
}
defer func() {
    if err := stop(); err != nil {
        t.Fatalf("stop() = %v", err)
    }
}()
```

組み立て後、まず 3 種類のリモートリソースがすべて登録されているか確認します：

```go
if _, ok := scarecrow.Find[*reporter.PushWatcher](hub); !ok {
    t.Fatal("push watcher is not registered")
}
if _, ok := metronome.Find[*reporter.SnapshotTicker](tickerHub); !ok {
    t.Fatal("snapshot ticker is not registered")
}
if _, ok := metronome.Find[*reporter.InjectionTicker](tickerHub); !ok {
    t.Fatal("injection ticker is not registered")
}
```

次に Farm の起動イベントを送信し、プッシュが実際にサーバーへ到達するのを待ちます：

```go
if err := hub.OnEvent(scarecrow.FarmStartEvent{
    Farm:        "farm",
    Plots:       []string{"root"},
    Edges:       map[string][]string{"root": {}},
    SourcePlots: []string{"root"},
    StartTime:   time.Now(),
}); err != nil {
    t.Fatalf("OnEvent(FarmStartEvent) = %v", err)
}

waitForPath(t, paths, "/api/push_graph_meta")
```

- `PushWatcher.OnEvent` はレコードをバックグラウンドキューに書き込むだけで、HTTP リクエストは `Spout` の消費 goroutine が発行します。そのため `waitForPath` で待つ必要があり、直ちにアサートすることはできません。
- エンドポイントのパスは `pushHandler.handle` の固定マッピングに由来します：`FarmStartEvent → /api/push_graph_meta`。テストはリクエストボディの内容をアサートしていません。
- `SnapshotPeriod` / `InjectionPeriod` は未設定（ゼロ値）で、それぞれ既定周期 1 / 5 にフォールバックします。テストはグラフメタ情報のプッシュのみを待ち、周期リクエストはアサートしません。待機中にスナップショット ticker が `/api/push_snapshot` を発行することがありますが、`waitForPath` は一致しないパスを無視します。
- `defer stop()` は組み立て成功直後に登録され、テスト終了前に消費ループとクロックを確実に停止します。イベント送信とアサーションはいずれも `defer` の実行前に完了します。

### `TestStartLocalResourcesRejectNilHub`

```go
tickerHub, err := metronome.NewHub()
if err != nil {
    t.Fatalf("NewHub() = %v", err)
}
if _, err := assembly.StartLocalResources(nil, tickerHub, "INFO", metronome.DefaultInterval); err == nil {
    t.Fatal("StartLocalResources(nil, tickerHub) = nil, want error")
}
```

- 「エラーがある」ことだけをアサートし、具体的なエラーメッセージには一致させないため、エラーメッセージの調整でテストが壊れることはありません。
- この経路はオブザーバーを作成する前に戻るため、ディレクトリやコルーチンの副作用は発生しません。

### `TestStartLocalResourcesRejectNilTickerHub`

```go
watcherHub := scarecrow.NewHub()
if _, err := assembly.StartLocalResources(watcherHub, nil, "INFO", metronome.DefaultInterval); err == nil {
    t.Fatal("StartLocalResources(watcherHub, nil) = nil, want error")
}
```

- ticker Hub が nil の 2 番目の事前検証をカバーし、同様にエラーの存在のみをアサートします。

### `TestStartReportResourcesRejectInvalidArguments`

```go
if _, err := assembly.StartReportResources(
    watcherHub,
    tickerHub,
    injectionTargetStub{},
    "INFO",
    metronome.DefaultInterval,
    nil,
); err == nil {
    t.Fatal("nil report config must be rejected")
}

if _, err := assembly.StartReportResources(
    watcherHub,
    tickerHub,
    nil,
    "INFO",
    metronome.DefaultInterval,
    &reporter.Config{URL: "http://example.com", SessionID: "farm@1"},
); err == nil {
    t.Fatal("nil injection target must be rejected")
}
```

- `report == nil` と `injectionTarget == nil` の 2 つの検証分岐を順にカバーします。
- どちらの呼び出しもオブザーバー登録前に行われるため、後続のケースを汚染しません（各テストもそれぞれ Hub を新規作成します）。

## 実行方法

```bash
# 运行本包全部测试
go test ./pkg/assembly/...

# 仅运行本地装配相关用例
go test ./pkg/assembly -run 'TestStartLocalResources'

# 仅运行远程装配相关用例
go test ./pkg/assembly -run 'TestStartReportResources'

# 查看详细结果
go test ./pkg/assembly -v
```

## 注意事項

- **実行時の生成物が残る**：`TestStartLocalResourcesAssembleLifecycle` は作業ディレクトリを設定しないため、`LifecycleRecordHandler` / `LogRecordHandler` は相対パスで `lifecycles/<日期>/grow_lifecycle(<时间>).sqlite3` と `logs/grow_log(<日期>).log` を作成します。位置は `go test` 実行時の作業ディレクトリ（通常は `pkg/assembly/`）です。テストにはクリーンアップ処理がなく、これらのディレクトリは `.gitignore` で無視されています。
- **カバーされていない経路**：
  - ログオブザーバーの内容（実際にファイルへ書き込まれるか、レベルフィルタが正しいか）はアサートしていません。
  - 指標オブザーバーの集計結果（`GetPlot` / `GetPlots`）はアサートしていません。
  - プッシュのリクエストボディ、リクエストメソッド、失敗時のリトライはアサートせず、パスの到達のみを検証しています。
  - スナップショット ticker と注入 ticker の周期発火、リモート注入インターフェースは駆動していません。
  - 起動失敗時の各種ロールバック分岐（`AddWatcher` / `AddTicker` / `Hub.Start` の失敗）はカバーしておらず、失敗する handler や書き込み不可のディレクトリを注入しないと発生させられません。
- **`StopFunc` のエラーは受け取る**：ローカルのケースは 2 回の `stop()` がいずれも `nil` を返すことを要求し、リモートのケースは `defer` 内で 1 回確認します。下位のフラッシュタイムアウトはテストを失敗させます。
- **エラーハンドラはオブザーバーの障害を即座に失敗させる**：`WithErrorHandler` 内の `t.Errorf` は現在のテストにのみ影響します。オブザーバーが panic した場合、Hub はそれをエラーに包んでこのハンドラに渡すため、同様に捕捉されます。
- **テストは実際のファイルシステムと SQLite ドライバに依存する**：`modernc.org/sqlite` が利用可能である必要があります。作業ディレクトリが書き込み不可またはディスクが満杯の場合、`Start` はエラーを返し、ローカルのケースが失敗します。
- **リモートのケースはローカルネットワークスタックに依存する**：`httptest.NewServer` はループバックアドレスで待ち受けます。実行環境がローカルポートの待ち受けを禁止している場合、リモートのケースは失敗します。
