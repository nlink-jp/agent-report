# agent-report

[gem-agent](https://github.com/nlink-jp/gem-agent) と
[lagent](https://github.com/nlink-jp/lagent) のセッションの利用概要を、
自分の機械の上で作り、報告に添えるための道具です。

> **開発中です。** コマンドラインはできていますが、記録の読み取りと集計はまだ
> 実装していません。計画は [RFP](docs/ja/agent-report-rfp.ja.md) を見てください。

[English README](README.md)

## なぜ

gem-agent や lagent の不具合を報告しても、開発側からはあなたの環境の規模や使い方が
見えません。セッションの記録にはあなた自身の作業が入っているので、そのまま送ることも
できません。agent-report はその記録を読み、決まった項目の数字だけを出します。
セッション数、ターンあたりのラウンド数、コンテキスト量、ツールごとの呼び出し回数と
所要時間、承認の判断、MCP のサーバー数とツール数などです。

- **数字だけ。** 記録の中の会話・コマンド・パス・宣言された目的などの自由記述は、
  出力に一切現れません。組み込みツールの名前は既知の一覧と照合し、一覧にないものは
  「その他」に数えます。MCP のサーバー名は `--mcp-names` を付けたときだけ出ます。
- **何も送らない。** ネットワークを使いません。出力を読んでから、あなた自身が渡します。
- **読むだけ。** 記録を書き換えたり、移動・削除したりしません。
- **記録にないことは「記録なし」と出す。** たとえば起動の仕方（`-p`、`--auto`）は
  記録に残っていないので、そのように表示します。

## 使い方

```sh
agent-report                          # 両ランタイム・直近 30 日・表で表示
agent-report --runtime lagent --since all
agent-report --json > report.json     # 同じ項目を JSON で
```

| フラグ | 既定 | 意味 |
|---|---|---|
| `--runtime gem-agent\|lagent\|all` | `all` | 集計するランタイム |
| `--since <n>d\|YYYY-MM-DD\|all` | `30d` | 集計期間 |
| `--json` | オフ | 表の代わりに JSON を出す |
| `--mcp-names` | オフ | MCP サーバー名ごとの呼び出し回数を出す |
| `--sessions-dir <path>` | 各ランタイムの既定 | 記録を別のディレクトリから読む（`--runtime` が 1 つのときだけ） |
| `--version` | | 版数を出す |

記録の既定の場所は `~/.local/state/gem-agent/sessions` と
`~/.local/state/lagent/sessions` です。

終了コード: `0` 成功、`1` 実行時のエラー、`2` 使い方の誤り。

## 対応環境

Apple Silicon の macOS のみです。読む対象の 2 つのランタイムと同じです。

## ビルド

```sh
make build    # dist/agent-report に出力
make test
```

## ライセンス

MIT — [LICENSE](LICENSE) を見てください。
