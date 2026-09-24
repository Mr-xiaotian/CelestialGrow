# pkg/farm/farm_start_test.go

> 📅 最終更新日: 2026/09/24

## 役割

`farm_start_test.go` は `Farm.Run` の最も単純な「線形」トポロジーにおけるエンドツーエンドの振る舞いをカバーする。単一のソース、単一のシンクで、`Run` が複数の種子を注入し、以下を検証する：

- すべての種子が処理される
- 下流の結果が上流の変換と一致する
- すべての plot が終了した後の状態が `done`（`int == 2`）である

## テストケース

### `TestFarmRunLinear`

- `root`（`seed * 2`、`WithTenders(2)`）と `head`（seed を収集、`WithTenders(2)`）を作成する。
- `Connect(root → head)`。
- `Run` 時に `root` へ `{1, 2, 3}` を注入する。
- 期待：
  - `head` は合計 3 つの seed を受信し、ソートすると `[2, 4, 6]` になる。
  - `root.GetState() == 2`、`head.GetState() == 2`（すなわち `Plot.notifyFinish` が発火済み）。
- `head` の内部では `sync.Mutex` で共有 `results` スライスを保護する。

## 重要な詳細

- **並行安全性**：テストにおける下流 `head` の cultivator は `results` をロックする。これは `pkg/plot` の `tend` コルーチンによる並行ディスパッチモデルと一致する。
- **状態機械の検証**：`GetState() == 2` は `Plot.sprout` が `WaitAsync` を既に抜けたことを示し、`Farm.Run` の `WaitAsync` の順序がすべての plot の完了後にのみ返ることを検証する。
- **source seal は検証しない**：`sourceNodes` は 1 つだけ（`root`）であり、その seal は `Run` の末尾で `Seal()` を通じて注入される。テストは最終状態を通じて、`Seal` が「上流が揃う前に強制終了する」回帰を引き起こさないことを間接的に確認する。

## 関連ソース

- `pkg/farm/farm.go` の `Run` フロー
- `pkg/plot/plot_base.go` の `Seed` / `Seal` / `sprout` / `tend`
- `pkg/plot/constant.go` の `sourceInput = "__input__"`

## 実行方法

```bash
go test ./pkg/farm/ -run 'TestFarmRunLinear' -v
```
