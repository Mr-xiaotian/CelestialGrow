# pkg/reporter/push_test.go

> 📅 最終更新日: 2026/11/26

## 役割

`pkg/reporter/push_test.go` はローカルの `httptest` サーバーでプッシュ経路全体（イベント → レコード → キュー → HTTP リクエスト）を検証します。特に**リクエスト順序**、**JSON フィールド名**、**フィールドの由来**（とくに `event_id` が `WeedID` を取ること、`error_type` が `%T` を使うこと）を固定し、あわせて `toJSONValue` の降格パスとコンストラクタの引数検証もカバーします。

## テスト分布

| テスト | カバー内容 |
|------|----------|
| `TestPushWatcherPushesFarmStartSnapshotAndShutdown` | エンドツーエンド：5 回のリクエストの順序、グラフメタ／エラー／スナップショット／オフライン化ペイロードのフィールド |
| `TestToJSONValueFallsBackToReadableText` | JSON シリアライズ不能なタスクペイロードが文字列に降格される |
| `TestNewPushWatcherRejectsInvalidArguments` | 空 SessionID、空 URL、nil MetricsView、scheme 欠落、非 http(s) scheme |

テスト補助：

```go
type capturedRequest struct {
    path string
    body []byte
}

func requestPaths(requests []capturedRequest) []string
```

`capturedRequest` はサーバーのハンドラ内でのみ追加されます。`Spout` が単一 goroutine で直列送信し、かつ `http.Client` が応答を待ってから返るため、追加は自然に直列になり、テストにロックは不要です。`metricsView` は `snapshot_test.go` の `stubMetricsView` をそのまま再利用します（同一パッケージのテストで共有）。

## 重要なアサーションと設計意図

### エンドツーエンドの順序：`TestPushWatcherPushesFarmStartSnapshotAndShutdown`

`root` ノードを 1 つだけ持つ `stubMetricsView`（`Status: PlotRunning`、`StartTime: time.Unix(5, 0)`、`InputTotal 3 / Processed 2 / Pending 1 / Succeeded 2`、`UpstreamCounts {}`、`DownstreamCounts {"leaf": 2}`）を構築し、続いて順に次をトリガーします：

| ステップ | 呼び出し | 期待されるリクエスト |
|:----:|------|---------|
| 1 | `OnEvent(FarmStartEvent{Farm:"demo", Plots:["root"], Edges:{"root":{"leaf"},"leaf":{}}, SourcePlots:["root"], NodeMeta:{"root":{ClassName:"SinglePlot",MaxWorkers:2}}, StartTime:Unix(10,0)})` | `/api/push_graph_meta` |
| 2 | `PushSnapshot({"root": {Status: 1}})` | `/api/push_snapshot` |
| 3 | `OnEvent(SeedWitherEvent{Plot:"leaf", Seed:{"id":1}, Err:errors.New("boom"), SeedID:7, WeedID:8, Time:Unix(15,0)})` | `/api/push_error` |
| 4 | `OnEvent(FarmEndEvent{Farm:"demo", EndTime:Unix(20,0)})` | `/api/push_snapshot` |
| 5 | `Stop()` | `/api/shutdown_session` |

最後に `reflect.DeepEqual` で**パスリスト全体**を比較します：

```go
[]string{
    "/api/push_graph_meta",
    "/api/push_snapshot",
    "/api/push_error",
    "/api/push_snapshot",
    "/api/shutdown_session",
}
```

これが本ファイルで最も重要なアサーションです：「FarmEnd が先に終態スナップショットを追加プッシュし、その後で shutdown を送る」こと、およびキューの FIFO + 単一の消費 goroutine がもたらす決定的な順序を同時に検証します。`Stop` はアサーションの前に置かれ、クローズ時にバッファが排出されることを保証します（`Spout.Stop` は残りのレコードを処理し終えます）。

#### グラフメタ情報ペイロード（`requests[0]`）

| アサーション | 意図 |
|------|------|
| `meta["session_id"] == "farm@1"` | セッション識別子がイベントではなく構築引数に由来する |
| `meta["graph"] == "demo"` | `graph` は `FarmStartEvent.Farm` を取る |
| `meta["node_meta"]` が `map[string]any` で `"root"` を含む | 構築期のメタ情報がグラフメタと一緒に 1 回で報告される |
| `edges["leaf"]` が `[]any` で長さ 0 | **空の隣接リストは `null` ではなく `[]` にエンコードされる**（`copyEdges` が非 nil の空スライスを使う） |

テストは `graph_mode`、`start_time`、`is_dag`、`nodes`、`source_nodes` の具体的な値を**アサートしていません**。また `node_meta.root` 内部の `class_name`/`max_workers` も確認していません。

#### エラーペイロード（`requests[2]`）

| アサーション | 意図 |
|------|------|
| `session_id == "farm@1"` | エラーレコードがセッション識別子を自身で持つ |
| `event_id == float64(8)` | 取っているのは `WeedID`（8）であり、`SeedID`（7）では**ない** |
| `node == "leaf"` | `SeedWitherEvent.Plot` を取る |
| `error_type == "*errors.errorString"` | `fmt.Sprintf("%T", Err)` によって生成される |
| `error_message == "boom"` | `fmt.Sprintf("%v", Err)` によって生成される |

`task_json` と `ts` はアサートされていません。JSON の数値は一律 `float64` にデコードされるため、`event_id` を比較するときは `float64(8)` と書く必要があります。

#### スナップショットとオフライン化（`requests[3]` / `requests[4]`）

- どちらも `session_id == "farm@1"` だけをアサートします。
- `requests[3]` は FarmEnd がトリガーした**終態スナップショット**（ステップ 2 の周期スナップショットではない）で、その `snapshot` の内容は `stubMetricsView` に由来しますが、テストはノード、カウント、`timestamp` を確認していません。
- `requests[4]` は shutdown ペイロードがセッション識別子を持つことを検証します：サーバーは `session_id` に基づいてどのセッションをオフラインにするかを判断するため、このフィールドが欠けるとセッションを回収できなくなります。

### 降格シリアライズ：`TestToJSONValueFallsBackToReadableText`

```go
value := make(chan int)
got, ok := toJSONValue(value).(string)
if !ok || got == "" {
    t.Fatalf("toJSONValue(chan) = %#v, want non-empty string", got)
}
```

- `chan int` は `encoding/json` でエンコードできないため、`toJSONValue` は `fmt.Sprintf("%+v", value)` で得られる非空の文字列に退化しなければなりません。
- 「型が string かつ非空」だけをアサートし、具体的なテキストは比較しません（チャネルのアドレスは実行ごとに異なります）。これは意図的です：降格の目標は「可読で送信可能」であり、安定した値ではないからです。
- 設計意図：1 つのタスクのシリアライズ問題でエラー報告全体が失敗してはいけません。

### 引数検証：`TestNewPushWatcherRejectsInvalidArguments`

| 入力 | 想定される拒否理由 |
|------|-------------|
| `("", "http://example.com", metricsView)` | 空の SessionID |
| `("demo@1", "", metricsView)` | 空の base URL |
| `("demo@1", "http://example.com", nil)` | nil MetricsView |
| `("demo@1", "127.0.0.1:5005", metricsView)` | scheme 欠落 |
| `("demo@1", "ftp://example.com", metricsView)` | 非 `http`/`https` |

すべてのケースは `err == nil` だけを判定し、エラーテキストは比較しません。検証順序により「空 SessionID + 空 URL」では SessionID のエラーが先に報告されますが、テストは各回で 1 つの引数だけを不正にしており、順序は検証していません。

## 実行方法

```bash
# 仅本文件的测试
go test ./pkg/reporter -run 'TestPushWatcher|TestToJSONValue|TestNewPushWatcher' -v

# 整个包
go test ./pkg/reporter -v
```

`-race` 付きで一度実行し、非同期経路にデータ競合がないことを確認することを推奨します：

```bash
go test ./pkg/reporter -race
```

## 注意事項

- **未カバーの失敗分離**：サーバーが 500 を返す、あるいは接続を切るケースがないため、「1 件のプッシュ失敗はログのみで、後続のレコードは送信され続ける」ことはコードレビューでのみ保証されます。
- **未カバーの非 2xx の 512 バイト切り詰め**：`post` の `io.LimitReader` と読み捨てロジックにはテストがありません。
- **未カバーの `Start` 前のエンキュー / `Stop` タイムアウト**：`shutdown timeout` の経路と `inlet send timeout` の経路にはいずれもケースがありません。テストは常に先に `Start` し、最後に `Stop` します。
- **未カバーの `graph_mode`（固定 `"grow"`）、タイムスタンプ、サブ秒精度**：`start_time`/`timestamp`/`ts` はいずれもアサートされておらず、`unixSeconds` の小数部に回帰保護がありません。
- **未カバーの空／未知のイベント**：`OnEvent` の `default` 分岐（`nil` を返し送信しない）にテストがありません。
- **リクエストボディの読み取り失敗は即 `t.Fatalf`**：サーバーのハンドラは body の読み取りに失敗するとテストを直接終了します。これはテストコードであり、本番経路ではありません。
- **アサーションは正確な順序に依存**：将来プッシュをマルチコルーチンの並列に変更すると、パスリストの比較は失敗します。これは意図された順序契約であり、安易に緩めるべきではありません。
