# pkg/farm/render_test.go

> 📅 最終更新日: 2026/09/24

## 役割

`render_test.go` は `package farm_test` のブラックボックス方式で、`pkg/farm/render.go` の公開関数 `RenderStructureList` をカバーし、レンダリング形状、正確な出力、境界と深いグラフを検証する。

## テストの重点

- **正確な出力形式**：枠の幅、整列のパディング、接続子は実装と一致しなければならない。
- **共有 / サイクルノードの重複排除**：同一のノードは 1 回だけ展開され、重複して現れる場合は `[Ref]` が付加される。
- **空グラフとソースノードの推定**：`nodes` が空のときのプレースホルダー。`sourceNodes` が空のときの推定ロジックと孤立ノードの補完。
- **深い連鎖の回帰**：5000 ノードの深い連鎖により、「明示的なスタックによる反復」実装が再帰の深さによってスタックオーバーフローを起こさないことを保証する。

## テストケース

| テスト関数 | 検証点 |
| --- | --- |
| `TestRenderStructureList_Basic` | `s1 → {s2, s3} → s4` の菱形。返り値が非空で、先頭行の内容に `s1` が含まれ、共有ノード `s4` に `[Ref]` が現れる |
| `TestRenderStructureList_SimpleExact` | `a → b` のレンダリング結果が期待値と行ごとに `DeepEqual`：`+-------+` / `\| a     \|` / `\| ╘-->b \|` / `+-------+` |
| `TestRenderStructureList_NoNodes` | `(nil, nil, nil)` は直接 `["+ No stages defined +"]` を返す |
| `TestRenderStructureList_Cycle` | `c1 → c2 → c3 → c1` のサイクル。`c1` はちょうど 2 回現れ（展開 + `[Ref]`）、`[Ref]` を含む |
| `TestRenderStructureList_DeepChain` | 5000 ノードの連鎖グラフ。返る行数は `deepChain + 2` で、先頭行に `n0`、末尾の内容行に `n4999` を含む |
| `TestRenderStructureList_OrphansAndInferredSources` | `sourceNodes` が空のとき自動的にルートを推定し、孤立ノード `a` がレンダリングされ、`b` のサブツリーが `╘-->c` をレンダリングする |

## 重要な詳細

- **`deepChain = 5000` 定数**：コメントはその目的を「一般的な再帰の深さ制限を超えること」と明記しており、明示的なスタックによる反復版のレンダリングロジックの回帰に用いられ、再帰実装への退行を防ぐ。
- **正確なアサーションと包含アサーション**：`SimpleExact` と `NoNodes` のみが `reflect.DeepEqual` で全量比較を行い、他のケースは `strings.Contains` / `strings.Count` で緩やかなアサーションを行い、不安定なノード順序へ結合するのを避ける。
- **推定されたルートの区切り**：`OrphansAndInferredSources` では推定された 2 つのルートの間に空行が挿入されるが、テストは主要な行の存在のみをアサートし、空行の位置は制約しない。

## 関連ソース

- `pkg/farm/render.go` の `RenderStructureList`
- `pkg/farm/farm.go` の `Farm.getStructureList`（生産側の呼び出し点）

## 実行方法

```bash
go test ./pkg/farm/ -run 'TestRenderStructureList' -v
```
