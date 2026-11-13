# demo/demo_farm/main.go

> 📅 最終更新日: 2026/11/13

## 役割

`demo/demo_farm/main.go` は CelestialGrow プロジェクトに同梱されている最も単純な `Farm` サンプルで、公開統一エントリ `pkg/api` を使って 2 つの `SinglePlot` からなる小さなパイプラインを構築し実行する方法を示します。

- `NewSinglePlot` で 2 つの並行処理ノード（`root` と `head`）を作成する
- `NewFarm` でスケジューリンググラフを作成し、ノードを登録する
- `Connect` で 2 つのノード間に上流下流の関係を構築する
- `Run` で初期 seed を注入し、グラフ全体を実行する

このパイプライン全体のセマンティクスは「root が seed を 2 倍にして head に渡し、head が 1 を加える」であり、入門デモとして最適な最小の閉ループです。

## コード構造

ファイルは 3 つの部分から構成されています。

1. **パッケージとインポート**：`package main` 形式で実行可能なエントリを提供し、`pkg/api` のみを別名 `grow` でインポートします。
2. **2 つの cultivator 関数**：`double` と `addOne` で、それぞれ `root` と `head` の処理ロジックになります。
3. **`main` 関数**：2 つの `SinglePlot` の構築、Farm の登録、接続、実行を順に完了します。

対応するデータフロー（2 ノード + 1 エッジ）は次のように簡略化できます。

```mermaid
flowchart LR
    S1[seed: 1,2,3] --> R[root<br/>double]
    R -->|fruit *2| H[head<br/>addOne]
    H --> O[head の出力]

    style R fill:#eef2ff,stroke:#6366f1,stroke-width:2px,color:#1e1b4b
    style H fill:#ecfeff,stroke:#0891b2,stroke-width:2px
```

## 主な呼び出し

`main.go` に登場する主要な API（構築と設定は `pkg/api` 由来、Farm のメソッドは `pkg/farm` が提供）：

| 呼び出し | 由来 | 役割 |
|------|------|------|
| `grow.NewSinglePlot[S, F](name, cultivator, opts...)` | `pkg/api` → `plot.NewSinglePlot` | 1 入力 1 出力のジェネリックな並行ノードを構築します。`S` は seed 型、`F` は fruit 型です。 |
| `grow.NewFarm(name)` | `pkg/api` → `farm.NewFarm` | Farm を構築し、内部でイベントクライアントとオブザーバー Hub を作成します。ログレベルは `Run` / `Start` で渡す方式に変更されました。 |
| `farm.AddPlot(plots ...plot.PlotNode)` | `pkg/farm` | 複数の `PlotNode` を Farm に登録し、トポロジグラフに追加します。 |
| `farm.Connect(fromPlots, toPlots []plot.PlotNode)` | `pkg/farm` | ソースグループとターゲットグループの間に完全接続（直積）を確立し、各接続について seed/fruit 型の検証を行います。 |
| `farm.Run(inputs map[string][]any, logLevel string)` | `pkg/farm` | グラフ全体を同期的に実行します。`logLevel` は今回の実行のログ最低レベル（空文字列は `INFO`）で、すべての Plot が完了するまでブロックします。 |
| `grow.WithTenders(n int)` | `pkg/api` → `plot.WithTenders` | Plot の並行 tender（世話をするコルーチン）数を設定します。既定は `runtime.NumCPU()` です。 |
| `grow.PlotNode` | `pkg/api` → `plot.PlotNode` | `Farm.Connect` の引数で使用される統一インターフェースで、`SinglePlot` / `SplitPlot` / `RoutePlot` のジェネリクスを消去しています。 |

## 主要なフロー

`main` 関数の内部は次の順序で実行されます。

1. `root` ノードを構築します。cultivator は `double`、並行 tender 数は 2 です。
2. `head` ノードを構築します。cultivator は `addOne`、並行 tender 数は 2 です。
3. `demo_farm` を作成します。`NewFarm` は Farm の構築（イベントクライアント、Hub、レジストリ、トポロジグラフ）のみを行い、ログレベルは受け取りません。
4. `AddPlot(root, head)`：2 つのノードを Farm に登録します。名前は一意でなければなりません。
5. `Connect({root}, {head})`：`root` → `head` の間に 1 本のエッジを確立します。
6. `Run({"root": {1, 2, 3}}, "INFO")`：3 つの初期 seed を `root` に注入し、グラフ全体の実行を開始します。`Run` は `pkg/assembly` を通じてログ、ライフサイクル、指標のオブザーバーを起動し、Farm Hub を各 Plot に登録したうえで、すべての Plot が完了するまで待機してから戻ります。

## 実行時の生成物

`Run` が戻った後、カレントワーキングディレクトリに 2 種類の生成物が生成（または追記）されます。

- `logs/grow_log(YYYY-MM-DD).log`：今回の Farm 実行で生成された構造化ログで、日ごとに追記されます。ログレベルは `Run` の第 2 引数 `"INFO"` で制御します。
- `lifecycles/YYYY-MM-DD/grow_lifecycle(HH-MM-SS.mmm).sqlite3`：今回の Farm のライフサイクルデータベースで、当日の日付を名前にしたサブディレクトリに置かれ、ファイル名には起動時刻が含まれます。

このうち sqlite データベースには 3 つのテーブルが含まれます。`events`（seed/fruit/weed/prune/seal イベント）、`event_parents`（イベントの親子エッジ）、`status`（各 seed の状態スナップショット。状態は `seed` / `ripen` / `wither` / `prune`）です。

> 上記のパスとファイル命名は `pkg/persist` が決定しており、本サンプルではカスタム設定を行っていません。クリーンアップが必要な場合は、`logs/` と `lifecycles/` ディレクトリを直接削除できます。

## 使用例

以下のコードは `demo/demo_farm/main.go` のソースと完全に一致しており、そのままコピーして実行できます。

```go
package main

import grow "github.com/Mr-xiaotian/CelestialGrow/pkg/api"

// double 将种子翻倍。
func double(num int) (int, error) {
	return num * 2, nil
}

// addOne 为种子加一。
func addOne(num int) (int, error) {
	return num + 1, nil
}

// main 演示一条 Farm 流水线：root 将种子翻倍后传给 head 加一。
func main() {
	root := grow.NewSinglePlot("root", double, grow.WithTenders(2))
	head := grow.NewSinglePlot("head", addOne, grow.WithTenders(2))

	farm := grow.NewFarm("demo_farm")
	if err := farm.AddPlot(root, head); err != nil {
		panic(err)
	}
	if err := farm.Connect([]grow.PlotNode{root}, []grow.PlotNode{head}); err != nil {
		panic(err)
	}
	if err := farm.Run(map[string][]any{
		"root": {1, 2, 3},
	}, "INFO"); err != nil {
		panic(err)
	}
}
```

### セクションごとの解説

- **インポート**：`pkg/api` のみをインポートし、別名 `grow` で参照することでローカル変数名 `farm` との衝突を避けます。
- **`double` / `addOne`**：最も単純な `func(S) (F, error)` 形式の cultivator 2 つで、戻り値の error は常に nil のため、サンプル内のすべての seed は正常に fruit を生成し、下流で消費されます。
- **`NewSinglePlot`**：2 つのノードの seed と fruit 型はいずれも `int` で、ジェネリックパラメータ `S = F = int` は Go コンパイラが自動的に推論します。`WithTenders(2)` は単一 Plot の並行 tender 数を 2 に制限します。
- **`NewFarm`**：Farm の構築（イベントクライアント、Hub、レジストリ、トポロジグラフ）のみを行い、ログ / ライフサイクルリソースは起動しません。これらのリソースは `Run` 時に `pkg/assembly` が一元的に組み立てます。
- **`AddPlot`**：`SinglePlot` を Farm の `plots` マップとトポロジグラフに追加し、Farm のイベントクライアントを共有します。名前が空の場合や重複登録の場合はエラーを返します。
- **`Connect`**：「グループ間完全接続」のセマンティクスを使用します。本例では両グループともノードが 1 つだけなので、`root → head` の有向エッジが 1 本だけ生成されます。
- **`Run`**：`inputs` を検証した後、ログ / ライフサイクル / 指標のオブザーバーを起動し、Farm Hub を各 Plot に登録し、すべての Plot を起動して seed を注入し、source ノード（ここでは `root`）に seal を送信し、すべての Plot の完了を待ってからリソースを停止して戻ります。

### 実行方法

プロジェクトのルートディレクトリで実行します。

```bash
go run ./demo/demo_farm
```

実行に成功すると次のようになります。

- ターミナルには何も出力されません：`double` / `addOne` に副作用はなく、ログは標準出力ではなくすべてファイルに書き込まれます。リソース回収段階のエラーも出力されません。
- カレントディレクトリに `logs/grow_log(YYYY-MM-DD).log` と `lifecycles/YYYY-MM-DD/grow_lifecycle(HH-MM-SS.mmm).sqlite3` の 2 種類の生成物が作成されます。

`INFO` レベルの場合、今回の実行でログファイルに書き込まれるメッセージはすべて `INFO`（グラフ構造、各 Plot の起動と停止、Farm の開始と終了）です。より低いレベルの `SeedInput`（`DEBUG`）と `SeedRipen`（`SUCCESS`）はフィルタリングされます。seed レベルの詳細を観察したい場合は、`Run` の `logLevel` を `"DEBUG"` に変更できます。

```go
if err := farm.Run(map[string][]any{
	"root": {1, 2, 3},
}, "DEBUG"); err != nil {
	panic(err)
}
```

## 注意事項

1. **依存関係の要件**：本サンプルはプロジェクトと同じ `github.com/Mr-xiaotian/CelestialGrow` モジュールに属するため、追加のインポートパスは不要です。`pkg/persist` は `modernc.org/sqlite` を介してライフサイクルデータベースに書き込みます。初回実行前に `go mod download` を実行して依存関係を準備しておくと確実です。
2. **`Run` は source ノードにのみ seal を送信する**：`inputs` は任意の登録済み Plot に初期 seed を注入できますが、`Run` が終了信号を送るのは source ノード（ここでは `root`）だけです。非 source ノード（ここでは `head`）の入力がいつ閉じられるかは、上流から転送される seal に依存します。したがって、設計上は source ノードにのみ seed を注入すべきです。
3. **エラー処理**：サンプルでは `panic(err)` 形式を使用していますが、これはデモ専用です。本番コードでは `AddPlot` / `Connect` / `Run` のエラーを上位に返して集中的に処理することを推奨します。
4. **実行は同期的にブロックする**：`farm.Run` はすべての Plot が完了するまでブロックし続けます。複数の Farm を並行して実行したい場合は、複数の goroutine からそれぞれ呼び出してください。
5. **オブザーバーと進捗出力**：本サンプルでは watcher を登録していないため、ターミナルにプログレスバーやイベント出力は表示されません。進捗を可視化したい場合は、`Run` の前に `pkg/scarecrow` をインポートし、`farm.AddWatcher(scarecrow.NewPrintWatcher("demo_farm"))` を呼び出してください。`Run` 中は `pkg/assembly` が `MetricsWatcher` を自動登録しているため、`GetFruitNum` / `GetWeedNum` / `GetPruneNum` などの計数メソッドは実行終了後に正常に読み取れます。
6. **型安全**：`Connect` は「上流の fruit 型」と「下流の seed 型」が一致するかどうかを検証し、一致しない場合はエラーを返します。本例では 2 つの Plot の S/F はいずれも `int` なのでエラーにはなりません。
7. **その他のノード型**：本サンプルでは `SinglePlot` のみを示しています。`pkg/api` は他に `NewSplitPlot`（1 入力多出力）と `NewRoutePlot`（名前による宛先指定ルーティング）もエクスポートしており、これらも `PlotNode` を実装しているため、`SinglePlot` と同じグラフ内で混在させて使用できます。
