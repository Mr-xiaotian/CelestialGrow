# pkg/reporter/inject_test.go

> 📅 最終更新日: 2026/11/26

## 役割

`pkg/reporter/inject_test.go` は `InjectionTicker` の 3 つの主要パスをカバーします：1 回の成功する取得と配分、`Start` 前の使用禁止、コンストラクタによる不正引数の拒否です。実際のサーバーには依存せず、すべて `net/http/httptest` とローカルのプローブで完結します。

## テスト分布

| テスト | カバー内容 |
|------|----------|
| `TestInjectionTickerPullsTasksAndTerminations` | `session_id` クエリパラメータ、2xx 応答のデコード、`tasks`/`terminations` の配分、`TickPeriod` の戻り値 |
| `TestInjectionTickerRejectsUseBeforeStart` | `Start` を呼ばずに `OnTick` を呼ぶと必ずエラーになり、リクエストを発行しないこと |
| `TestNewInjectionTickerRejectsInvalidArguments` | 空 URL、空 SessionID、nil target、scheme 欠落、非 http(s) scheme |

テスト補助型：

```go
type injectionTargetProbe struct {
    tasks        map[string][]any
    terminations []string
}
```

`injectionTargetProbe` は `InjectionTarget` を実装します：`InjectTasks` は map をそのまま保存し（コピーしない）、`InjectTerminations` は `append([]string(nil), nodes...)` でスライスをコピーします。それ自体はエラーを返さないため、「ターゲットの注入失敗」分岐はテストできません。

## 重要なアサーションと設計意図

### 成功する取得：`TestInjectionTickerPullsTasksAndTerminations`

サーバーは `httptest.NewServer` でリクエストを受け取り、ハンドラがまずアサートします：

```go
if got := r.URL.Query().Get("session_id"); got != "demo@1" {
    t.Fatalf("session_id = %q, want demo@1", got)
}
```

これにより `url.QueryEscape` のエンコード（リクエスト行では `demo%401`）と、サーバー側でデコードした値の等価性が検証されます。続いて次を返します：

```json
{"tasks": {"root": [1.0, "x"]}, "terminations": ["leaf"]}
```

クライアント側のアサーション：

| アサーション | 意図 |
|------|------|
| `NewInjectionTicker(...)` がエラーなし | `baseURL` が `httptest` の `http://127.0.0.1:<port>` であり、scheme/host の検証を通過できる |
| `TickPeriod() == 5` | 明示的に渡した周期が既定値で上書きされない |
| `Start()` / `Stop()` が `nil` を返す | ライフサイクルメソッドがエラーを生まない |
| `reflect.DeepEqual(target.tasks, map[string][]any{"root": {1.0, "x"}})` | JSON の数値が `float64` にデコードされ、map の構造と値が完全に一致する |
| `reflect.DeepEqual(target.terminations, []string{"leaf"})` | 終了リストが元の順序・内容で渡される |

`Stop` は `defer` に置かれ、再度 `nil` が返ることをアサートします。これにより「`Stop` が `OnTick` の後に安全に呼べる」ことが検証されます。テストは `InjectTasks` と `InjectTerminations` の**呼び出し順序**をアサートしていません（プローブは結果のみを記録します）。順序の保証は実装内の 2 回の逐次呼び出しによって提供されます。

### 未起動での使用：`TestInjectionTickerRejectsUseBeforeStart`

```go
ticker, err := NewInjectionTicker("http://example.com", "demo@1", &injectionTargetProbe{}, 1, time.Second)
// ...
if err := ticker.OnTick(metronome.TickEvent{Seq: 1}); err == nil {
    t.Fatal("OnTick() before Start must fail")
}
```

- 到達不能な `example.com` を使うのは意図的です：実装が誤って `OnTick` 内でクライアントを遅延作成した場合、テストは黙って通過するのではなく実際のネットワークリクエストを試みます。
- 「エラーがある」ことだけをアサートし、エラーテキストは検証しません。現在の実装は `reporter: injection client is not initialized` を返します。
- `client` が一度も作成されないため、`Stop` は呼びません。

### 構築引数の検証：`TestNewInjectionTickerRejectsInvalidArguments`

| 入力 | 想定される拒否理由 |
|------|-------------|
| `("", "demo@1", probe, 1, 1s)` | 空の base URL |
| `("http://example.com", "", probe, 1, 1s)` | 空の session ID |
| `("http://example.com", "demo@1", nil, 1, 1s)` | nil target |
| `("127.0.0.1:5005", "demo@1", probe, 1, 1s)` | scheme 欠落（`url.Parse` 後に host が空） |
| `("ftp://example.com", "demo@1", probe, 1, 1s)` | 非 `http`/`https` scheme |

テストは `err == nil` だけを判定し、具体的なエラーテキストは比較しません。5 つのケースはすべて `period = 1`、`timeout = 1s` を使うため、周期／タイムアウトの検証と干渉しません。

## 実行方法

```bash
# 仅本文件的测试
go test ./pkg/reporter -run 'TestInjectionTicker|TestNewInjectionTicker' -v

# 整个包
go test ./pkg/reporter -v
```

テストはローカルの `httptest` サーバー（ループバックアドレス）を起動し、外部の報告サービスを必要としません。共有状態もないため、`-race` 付きで実行できます：

```bash
go test ./pkg/reporter -run 'TestInjectionTicker' -race
```

## 注意事項

- **未カバーの検証分岐**：`period < 0`（`injection period must be positive`）と `timeout < 0`（`pull timeout must be non-negative`）はテストされていません。既定値の置換（`period == 0 → 5`、`timeout == 0 → 3s`）も公開 API からは観測できず、間接的にしか検証できません。
- **未カバーのエラーパス**：非 2xx ステータス、不正な JSON、`InjectTasks` の失敗（このとき `InjectTerminations` を呼んではならない）、`InjectTerminations` の失敗。
- **未カバーの空集合正規化**：サーバーが `{"tasks": null, "terminations": null}` を返したとき、ターゲットは空の map / 空のスライスを受け取るべきですが、現在のテストは非空のペイロードのみをカバーしています。
- **未カバーの URL 正規化**：`baseURL` の末尾に `/` が付くケースはありません。実装は末尾のスラッシュをすべて除去し、`//api/pull_injection` の連結を避けます。
- **テストはリクエストメソッドを検証しません**：サーバーのハンドラが `r.Method` を確認しないため、実装が誤って POST を使っても発見できません（現在の実装は `GET`）。
