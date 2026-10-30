# agent-report

[gem-agent](https://github.com/nlink-jp/gem-agent) と
[lagent](https://github.com/nlink-jp/lagent) のセッションの利用概要を、
自分の機械の上で作り、報告に添えるための道具です。

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
  記録に残っていないので、そのことと理由を出力に書きます。

## インストール

```sh
brew install nlink-jp/tap/agent-report
```

または [リリースページ](https://github.com/nlink-jp/agent-report/releases) から
`agent-report-v<version>-darwin-arm64.zip` を取得してください（Developer ID 署名・
Apple の公証済み）。

## 使い方

```sh
agent-report                          # 両ランタイム・直近 30 日・表で表示
agent-report --runtime lagent --since all
agent-report --json > report.json     # 同じ項目を JSON で
agent-report version                  # --version と同じ
```

| フラグ | 既定 | 意味 |
|---|---|---|
| `--runtime gem-agent\|lagent\|all` | `all` | 集計するランタイム |
| `--since <n>d\|YYYY-MM-DD\|all` | `30d` | 集計期間（日付はローカル時刻の 0 時） |
| `--json` | オフ | 表の代わりに JSON を出す |
| `--mcp-names` | オフ | MCP サーバー名ごとの呼び出し回数を出す |
| `--sessions-dir <path>` | 各ランタイムの既定 | 記録を別のディレクトリから読む（`--runtime` が 1 つのときだけ） |
| `--version` | | 版数を出す |

記録の既定の場所は `~/.local/state/gem-agent/sessions` と
`~/.local/state/lagent/sessions` です。既定の場所がないランタイムは `missing` と
出します。`--sessions-dir` で指定した場所がないときはエラーになります。

終了コード: `0` 成功、`1` 実行時のエラー、`2` 使い方の誤り。

## 出力の中身

先頭に `schema`（出力形式の版）、`tool`、`tool_version`、`period`（`since` と
`until`。UTC で書くので、あなたのタイムゾーンは出力に載りません）を出します。
続いてランタイムごとに次の項目を出します。

| 節 | 項目 |
|---|---|
| `transcripts` | `found` か `missing`（パスそのものは出しません） |
| `sessions` | 期間内に動きのあったセッション数、再開の回数 |
| `versions`、`models` | ランタイムの版数ごと・モデルごとのセッション数 |
| `turns` | 操作者のターン数と、ターンあたりのラウンド数（n / p50 / p90 / 最大） |
| `context` | セッションごとの最大プロンプト、プロンプトのうちキャッシュされた割合、会話圧縮の回数 |
| `tokens` | 呼び出し元ごと（主ループ、リスク評価など）の呼び出し数とトークン数 |
| `tools` | ツールを呼んだ応答の数、複数を呼んだ応答の数、組み込みツールごとの回数と所要秒数、不明な名前への呼び出し、MCP の呼び出し数・サーバー数・ツール数・所要秒数 |
| `shell` | レーン別（read / write / operator / unconfined / unrecorded / other）のシェル呼び出し数、タイムアウト |
| `approvals` | 自動承認の判断（段階別）、承認プロンプトでの判断（出どころ別）、フックによる拒否 |
| `interventions` | ラウンド上限・ループ検出による介入 |
| `modes` | 自動承認・読み取り専用で始まったセッション数（再開を含めて 1 セッション 1 回）、モード切替の回数 |
| `empty_model_responses` | 空で返ってきたモデルの応答の数 |
| `not_recorded` | 記録に残っていない事実と、その理由 |
| `anomalies` | 読めなかったディレクトリ・ファイル・行、ヘッダのないファイル、未対応の schema、未知の記録の種類、対応する呼び出しのないツール結果 |

記録の中の文字列は、決まった集合に入るものだけを出します。ランタイムが書く列挙値、
既知の組み込みツール名、狭いパターンに合う版数・モデル名・MCP サーバー名です。
版数はリリースのタグか `git describe` の出力に限り（コミットハッシュだけのものは
`dev` と出します）、モデル名は発行元の前置きが高々 1 つ（`google/…`）の ID に
限ります。それ以外は `other`（欄そのものがないときは `unrecorded`）に数えます。
あなたが自分で付けたモデル ID は書いたとおりに出るので、渡す前に `models` の節を
確かめてください。

ターンは、あなたが打ったメッセージで始まります。ランタイムが自動で入れるメッセージ
（セッションの事実、再開の注記、`!` のシェル実行、中断した呼び出しについての注記）
ではターンを始めません。

ランタイムは 1 つの応答に含まれる呼び出しを順に実行します。呼び出しの所要秒数は、
同じ応答の直前の呼び出しが終わった時点（最初の呼び出しは応答そのもの）から、その
呼び出しの結果までの時間です。承認待ちや `ask_user` への回答を待つ時間も含みます。

表と `--json` は同じデータから作ります。表は JSON を字下げした行に並べたものです。

## 対応環境

Apple Silicon の macOS のみです。読む対象の 2 つのランタイムと同じです。

## ビルド

```sh
make build    # dist/agent-report に出力
make test
make vuln     # govulncheck
```

## 文書

- [RFP](docs/ja/agent-report-rfp.ja.md) — 仕様と、その背景にある判断

## ライセンス

MIT — [LICENSE](LICENSE) を見てください。
