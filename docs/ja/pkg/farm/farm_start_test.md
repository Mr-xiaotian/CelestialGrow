# pkg/farm/farm_start_test.go

> 📅 最終更新日: 2026/11/13

## 役割

`farm_start_test.go` は `Farm.Run` の最も単純な「線形」トポロジーにおけるエンドツーエンドの振る舞いをカバーする。単一のソース、単一のシンクで、`Run` の内部では `InjectTasks` により種子を注入し、`InjectTerminations` により source を終了させ、以下を検証する：

- すべての種子が処理される
- 下流の結果が上流の変換と一致する
- すべての plot が終了した後の状態が `done`（`int == 2`）である

## テストケース

### `TestFarmRunLinear`

- `root`（`seed * 2`、`WithTenders(2)`）と `head`（seed を収集、`WithTenders(2)`）を作成する。
- `Connect(root → head)`。
- `Run` 時に `root` へ `{1, 2, 3}` を注入し、ログレベルには `"INFO"` を渡す。
- 期待：
  - `head` は合計 3 つの seed を受信し、ソートすると `[2, 4, 6]` になる。
  - `root.GetState() == 2`、`head.GetState() == 2`（すなわち `notifyFinish` が発火済み）。
- `head` の内部では `sync.Mutex` で共有 `results` スライスを保護する。

## 重要な詳細

- **並行安全性**：テストにおける下流 `head` の cultivator は `results` をロックする。これは `pkg/plot` の `tend` コルーチンによる並行ディスパッチモデルと一致する。
- **状態機械の検証**：`GetState() == 2` は `sprout` が既に終了し `WaitAsync` も返ったことを示し、`Farm.start` の待機順序を検証する。
- **成功経路のみをカバー**：`Run` が終了のフォールバックを通るのは注入失敗のときだけである；本ケースではすべての種子の型が一致するため、正常な「種子注入 → source 終了 → 待機」の経路を検証する。
- **source seal**：`sourceNodes` は `root` のみであり、`Run` が自動的にそれへ `Seal()` を呼ぶ；テストは最終状態を通じて「上流が揃う前に強制終了する」回帰が起きないことを間接的に確認する。

## 関連ソース

- `pkg/farm/farm.go` の `Run` / `start` / `InjectTasks` / `InjectTerminations`
- `pkg/plot/plot.go` の `Seed` / `Seal` / `sprout` / `tend`
- `pkg/plot/constant.go` の `sourceInput = "__input__"`

## 実行方法

```bash
go test ./pkg/farm/ -run 'TestFarmRunLinear' -v
```
