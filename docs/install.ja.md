# インストールと使い方

[English](install.en.md) · [한국어](install.ko.md) · [日本語](install.ja.md)

このページでは、Ratatosk のリリース変更点を AI エージェントから利用する 4 つの
方法を説明します。何もインストールせずホスト型エンドポイントに接続する
方法、手元の PC で stdio サーバーを動かす方法、Kubernetes クラスタに Helm
でデプロイする方法、そして kagent と統合する方法です。クライアントごとの
登録方法は[クライアントへのエンドポイント登録](#クライアントへのエンドポイント登録)
と[その他の stdio クライアント](#その他の-stdio-クライアント)にまとめています。

**MCP（Model Context Protocol）** は、AI エージェントが外部ツールを呼び出す
ときに使う標準プロトコルです。ratatosk-mcp はこのプロトコルで 6 つの
ツールを提供するサーバーです。`check_stack`（稼働中のバージョン一覧を受け取って
サーバープロセスの中で比較し、アップグレード経路ごとにアップグレード前の
確認事項・判断が必要な変更・バージョン番号の飛びを返す）、
`check_upgrade`（1 プロジェクトの 1 回のアップグレードをサーバーが計算）、
`get_release`（リリース 1 件の情報一式）、`list_releases`（最近のリリース要約）、
`search`（アドバイザリ番号・フラグ・テキストで検索）、
`list_projects`（追跡プロジェクトとそのスラッグ）です。Claude、ChatGPT、
Grok、Claude Code、Codex、Gemini CLI、Antigravity、kagent、自作の SDK
エージェントなど、どの MCP クライアントからでも接続できます。

## 実行方法を選ぶ

| 利用シーン | 方法 | セクション |
| --- | --- | --- |
| 何もインストールせずにまず試したい | ホスト型エンドポイントの URL を登録 | [ホスト型](#ホスト型エンドポイント) |
| 手元の PC で Claude Code、Claude Desktop、Codex、Gemini CLI などの stdio MCP クライアントを使う | `docker run`（stdio） | [ローカル](#ローカルstdio) |
| フレームワークを問わず、独自エージェント、CI ジョブ、SDK クライアントを Kubernetes 上で運用 | Helm チャート | [クラスタ内デプロイ](#クラスタ内への単独デプロイhelm) |
| [kagent](https://kagent.dev) を使用中（0.x または 1.x） | `kagent.enabled=true` の Helm オプション、またはマニフェスト | [kagent 統合](#kagent-統合) |

4 つの方法はいずれも同じ 6 つのツールで同じ公開データを提供し、どのモード
でもアカウントも API キーも不要です。違いは 3 つあります。

- **`check_stack` に渡した稼働中のバージョンがどこまで送られるか。** セルフホスト
  ではバージョン比較が利用者のプロセス内で完結し、そのバージョンはインフラ
  を離れません。ホスト型ではバージョンがサーバーメモリを通過しますが、
  どのログにも記録されません。
- **どのリクエスト上限がかかるか。** ホスト型では呼び出し元ごとに毎分
  60 リクエストまでで、サーバーのアップストリームの枠はホスト型の呼び出し元
  全員で分け合います。セルフホストは自分の IP の枠（アップストリームへ毎分
  1200 リクエスト）を使います。
- **監査ストリームを残せるか。** セルフホストのみ可能です。

どの方法でも、`get_release` のように特定バージョンを引数に取るツールは、
そのバージョンをアップストリームのリクエストパスに載せて取得します。取得対象を
名指しする値だからです。ただしそのパスもサーバー側のログには残りません。アクセスログが記録前にクエリ文字列を
除去し、`/v2/releases/…`・`/v2/upgrade/…` のパスをプレフィックスだけに
縮めるためです。詳細は各セクションで説明します。

## 前提条件

- **ホスト型**：リモート MCP サーバー（Streamable HTTP、ストリーミング
  方式の HTTP トランスポート）に対応したクライアントがあれば十分です。
  ほかに準備するものはありません。
- **ローカル（stdio）**：Docker。ソースからビルドするなら Go 1.26 以上。
- **例に出てくる `claude mcp add` コマンド**：[Claude Code](https://claude.com/claude-code)
  CLI（`claude` コマンド）がインストールされている必要があります。別の MCP
  クライアントを使う場合はそれぞれの登録方法に従えばよいので、必須では
  ありません。
- **クラスタ（Helm）**：Kubernetes クラスタと Helm 3、そしてこのリポジトリの
  クローン。チャートは別のチャートリポジトリや OCI レジストリには公開されて
  おらず、リポジトリの中にだけあります。
- **アウトバウンド HTTPS（egress）**：セルフホストではどの方法でも、サーバー
  プロセスが `ratatosk.io:443` へのアウトバウンド HTTPS 接続を開ける必要が
  あります。NetworkPolicy や egress プロキシでアウトバウンドを制限している
  クラスタでは、この宛先を許可リストに追加してください。プロキシやミラーを
  経由する必要があるなら、`RATATOSK_URL`（チャート値：`ratatoskUrl`）で
  アップストリームのアドレスを変更できます。遮断されたまま実行するとツールは
  エラーを返します（`check_stack` は「could not read the project list」）。
  一部の取得だけが失敗した場合、`check_stack` は応答を返し、影響を受けた
  コンポーネントの `note` に `fetch failed: …` を入れます。
  [トラブルシューティング](#トラブルシューティング)を参照してください。
- **kagent 統合**：kagent がすでにインストールされたクラスタ（kagent CRD を
  含む）。kagent 1.x では、サンプルエージェントを動かす Harness も必要です
  （[kagent 1.x](#kagent-1x) 参照）。

## ホスト型エンドポイント

`https://ratatosk.io/mcp` は、インストールなしですぐ使えるリモート MCP
エンドポイントです。Streamable HTTP を使うステートレス方式です。
セッションがないため、`Mcp-Session-Id` の往復なしに
リクエスト 1 つひとつが独立して処理されます。

### クライアントへのエンドポイント登録

URL はどのクライアントでも同じで、入力する場所だけが違います。

| クライアント | `https://ratatosk.io/mcp` を登録する場所 |
|---|---|
| Claude（ウェブ・デスクトップ） | Customize → Connectors → + Add → Add custom connector でこのアドレスを入れます。Team・Enterprise プランでは、オーナーが Organization settings → Connectors で追加し、メンバーは Customize → Connectors で接続します（[ドキュメント](https://support.claude.com/en/articles/11175166-getting-started-with-custom-connectors-using-remote-mcp)） |
| ChatGPT（ウェブ） | chatgpt.com/plugins で + を押して Add custom MCP server を選び、このアドレスをサーバー URL に入れます。認証はなしです（[ドキュメント](https://developers.openai.com/api/docs/guides/custom-mcp-server)） |
| Grok | grok.com/connectors で New Connector → Custom を選び、このアドレスを入れます（[ドキュメント](https://docs.x.ai/grok/connectors)） |
| Claude Code | `claude mcp add --transport http ratatosk https://ratatosk.io/mcp`（[ドキュメント](https://code.claude.com/docs/en/mcp)） |
| Grok Build | `grok mcp add --transport http ratatosk https://ratatosk.io/mcp`（[ドキュメント](https://docs.x.ai/build/features/mcp-servers)） |
| Codex | `~/.codex/config.toml` に書きます。`[mcp_servers.ratatosk]` テーブルに `url = "https://ratatosk.io/mcp"`（[ドキュメント](https://learn.chatgpt.com/docs/extend/mcp)） |
| Gemini CLI | `~/.gemini/settings.json` に書きます。`mcpServers.ratatosk` の下に `"httpUrl": "https://ratatosk.io/mcp"`（[ドキュメント](https://geminicli.com/docs/tools/mcp-server/)） |
| Antigravity | `~/.gemini/config/mcp_config.json` に書きます。`mcpServers.ratatosk` の下に `"serverUrl": "https://ratatosk.io/mcp"`（[ドキュメント](https://antigravity.google/docs/mcp)） |
| kagent | `RemoteMCPServer` リソースとして登録します。[kagent 統合](#kagent-統合)を参照してください |

Gemini CLI と Antigravity はどちらも `~/.gemini` の下のファイルを読みますが、
アドレスのキーは Gemini CLI が `httpUrl`、Antigravity が `serverUrl` です。
Claude Code では：

```bash
claude mcp add --transport http ratatosk https://ratatosk.io/mcp
```

### 確認

クライアントがなくても、curl でエンドポイントに接続できるか確認できます：

```bash
curl -s -X POST https://ratatosk.io/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1.0"}}}'
```

応答は SSE（Server-Sent Events）フレーミングで返ってきます。
`Content-Type` が `text/event-stream` でも失敗ではありません。`data:` 行の
JSON に `"serverInfo"` と `"name":"ratatosk"` が見えれば正常です：

```
event: message
data: {"jsonrpc":"2.0","id":1,"result":{…,"serverInfo":{"name":"ratatosk","version":"1.0.0"}}}
```

Claude Code なら `claude mcp list` の出力で確認してください：

```
ratatosk: https://ratatosk.io/mcp (HTTP) - ✔ Connected
```

### 既知の挙動と制限

- **SSE の GET は 405**：ステートレスモードはサーバー発の通知ストリーム
  （GET で開く SSE）を提供しないため、405 応答が正常な動作です。ブラウザの
  アドレスバーで開くと案内ページ（`/docs/mcp`）にリダイレクトされます。
- **適正利用について**：ホスト型エンドポイントは**呼び出し元ごとに毎分
  60 リクエスト**を許可します。全体で分け合う枠ではなく呼び出し元の
  アドレスごとに数えるので、誰か 1 人が忙しくても他の利用者が枯渇しません。
  MCP のメッセージはすべて数えます。クライアントは接続に 2 つか 3 つ
  （`initialize`、`notifications/initialized`、`tools/list`）を使い、そのあとは
  ツール呼び出し 1 回につき 1 つです。超えると `Retry-After` 付きの `429` が
  返ります。アップストリーム側では、ツール呼び出し 1 回が多数の API リクエスト
  になることがあります（`check_stack` はコンポーネントごとにそのプロジェクトの
  最近のリリースをすべて読みます）。この負荷はサーバー側で処理され、利用者
  ごとの毎分 60 リクエストの上限には含まれません。直接 `/v2` を呼ぶ場合は自分の IP の枠として毎分
  1200 リクエストが使えます。ポーリングのような負荷の高い使い方
  が必要なら、セルフホストに切り替えてください。

### プライバシー：ホスト型エンドポイントが記録するもの

ホスト型では、`check_stack` の引数（稼働中のバージョン一覧）がサーバー
メモリを通過しますが、リクエスト内容はどこにも記録されません：

- MCP サーバーのログには起動行とアップストリームのエラー行だけが残り、
  正常なリクエストは記録されません。エラー行にもリクエスト引数は
  含まれません。
- アップストリーム API のリクエストログは、呼び出し側が `traceparent` を
  送った場合にのみ 1 行書かれ、その行にあるのも正規化されたエンドポイント
  ラベルと trace_id だけです。パス・クエリは記録されません。
- サーバー前段のアクセスログはクエリ文字列を除去し、スラッグ・バージョンが
  パスに載る `/v2/releases/…`・`/v2/upgrade/…` のパスはプレフィックスだけを
  残し、IP をマスクし、リクエストボディの記録項目自体がありません。サーバーの
  ローカルでは 7 日以内にローテーションされ、写しが運営者の中央ログストアにも
  送られます。そちらの保持期間も 7 日です。
- 後述の監査ストリームはホスト型では無効で、有効にすることはありません。

ただし、Ratatosk の管理が及ばない境界が 1 つあります。ratatosk.io は
Cloudflare の背後にあり、TLS は Cloudflare で終端されるため、リクエストの内容
（ホスト型の `check_stack` 呼び出しに含まれるバージョンを含む）は転送の途中で
Cloudflare を通過し、Cloudflare のポリシーに従います。この境界が要件に合わない
場合はセルフホストを使ってください。`check_stack` に渡したバージョンがプロセスの
外に出なくなります。

## ローカル（stdio）

stdio は、MCP クライアントがサーバーを子プロセスとして起動し、標準入出力で
やり取りする方式です。手元の PC で Claude Code、Claude Desktop、Codex、
Gemini CLI、Antigravity と一緒に使うときの基本の選び方です。

### Claude Code

```bash
claude mcp add ratatosk -- docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0
```

`1.0.0` の部分には任意の
[リリースタグ](https://github.com/basphere/ratatosk-mcp/releases)を
指定できます。常に最新を追うなら `latest` を使ってください。
[バージョン固定](#バージョン固定)を参照してください。

### Claude Desktop

Claude Desktop は CLI の代わりに設定ファイル
（`claude_desktop_config.json`）で MCP サーバーを登録します。`mcpServers`
に次を追加してください：

```json
{
  "mcpServers": {
    "ratatosk": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "ghcr.io/basphere/ratatosk-mcp:1.0.0"]
    }
  }
}
```

設定ファイルの場所は OS ごとに異なるため、Claude Desktop のドキュメントを
参照してください。インストールなしで使うなら、上の
[ホスト型エンドポイント](#ホスト型エンドポイント)をリモート
コネクタとして登録しても構いません。

### その他の stdio クライアント

以下のクライアントはどれも同じコマンド
`docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0` を実行します。違うのは
書く場所だけです。

| クライアント | 場所 |
|---|---|
| ChatGPT デスクトップアプリ | 設定 → MCP servers → Add server で STDIO を選び、コマンドを入れます。Codex と `~/.codex/config.toml` を共有します（[ドキュメント](https://learn.chatgpt.com/docs/extend/mcp)） |
| Codex | `~/.codex/config.toml` に書きます。`[mcp_servers.ratatosk]` に `command = "docker"`、`args = ["run", "--rm", "-i", "ghcr.io/basphere/ratatosk-mcp:1.0.0"]`、`startup_timeout_sec = 60`（初回の実行でイメージを取得するため） |
| Gemini CLI | `~/.gemini/settings.json` に書きます。上の Claude Desktop と同じ `mcpServers` ブロックです |
| Antigravity | `~/.gemini/config/mcp_config.json` に書きます。上の Claude Desktop と同じ `mcpServers` ブロックです |
| Grok Build | `grok mcp add ratatosk -- docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0` |

ChatGPT（ウェブ）と grok.com は手元のマシンのコマンドではなく URL に接続するため、
ホスト型エンドポイントを登録します。

### ソースからビルド

Docker なしでバイナリとして使うには：

```bash
go build -o ratatosk-mcp .
claude mcp add ratatosk -- /path/to/ratatosk-mcp
```

stdio に対応した MCP クライアントであれば、いずれも同じ手順です。

### 確認

```bash
claude mcp list
# ratatosk: docker run -i --rm ghcr.io/basphere/ratatosk-mcp:1.0.0 - ✔ Connected
```

そのうえで、エージェントにこう聞いてみてください：

> 「envoy v1.36.8 と istio 1.30.1 を運用中。アップグレード前に対応すべき
> ことはある？」

エージェントが `check_stack` を呼び、現行バージョン以降のセキュリティ修正・
削除・デフォルト値の変更を、根拠の引用とともに挙げて答えれば成功です。

## クラスタ内への単独デプロイ（Helm）

`MCP_HTTP_ADDR` を設定すると、同じバイナリが `/mcp` で Streamable HTTP の
MCP を提供します（`/healthz` ヘルスチェック付き）。チャートはこれを
ClusterIP Service としてデプロイし、`MCP_HTTP_ADDR` を `service.port`
（デフォルト 8080）に合わせて自動設定します。クラスタ内のどのクライアントも、
プロセスを起動する代わりに URL で接続できます。SDK で作った独自
エージェント、ほかのエージェントフレームワーク、アップグレード前に
`check_stack` でゲートをかける CI ジョブなど、いずれも同じです。

### インストール

```bash
git clone https://github.com/basphere/ratatosk-mcp
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp
```

RBAC もシークレットも不要で、Pod Security Standards（PSS）の `restricted`
プロファイルでも警告なしに動作します。チャートの値はすべて
[チャート README](../charts/ratatosk-mcp/README.ja.md) に載っています。
MCP 仕様 2026-07-28 リビジョンを使う最新クライアント
（`Mcp-Protocol-Version: 2026-07-28` ヘッダーを送るクライアント）を HTTP
で受け付ける場合や、レプリカを 2 つ以上に増やす予定がある場合は、
`--set statelessHttp=true` を有効にしてください。
[設定リファレンス](#設定リファレンス)を参照してください。

### 確認

Pod が起動しているか：

```bash
kubectl get pods -l app.kubernetes.io/name=ratatosk-mcp
# NAME                            READY   STATUS    RESTARTS   AGE
# ratatosk-mcp-6f7b9c8d4-x2m5q    1/1     Running   0          30s
```

起動ログの 1 行に、待ち受けアドレスとアップストリームが出力されます：

```bash
kubectl logs deploy/ratatosk-mcp
# {"time":"…","level":"INFO","msg":"listening","service":"mcp","transport":"http","addr":":8080/mcp","mode":"stateful","upstream":"https://ratatosk.io","version":"1.0.0","rate_limit":"off","max_inflight":"off"}
```

ヘルスチェックまで確認するなら：

```bash
kubectl port-forward svc/ratatosk-mcp 8080:8080 &
sleep 2   # port-forward の準備が整う前に curl を実行すると connection refused になります
curl -i http://localhost:8080/healthz
# HTTP/1.1 200 OK
```

確認が終わったら、バックグラウンドの port-forward を `kill %1` で終了して
ください。

### 接続

クラスタ内の MCP クライアントには、次の URL を指定してください：

```
http://ratatosk-mcp.<namespace>.svc:8080/mcp
```

### アップグレード

```bash
git -C ratatosk-mcp pull
helm upgrade ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp
```

インストール時に `--set` で値を変えていた場合は、アップグレードでも同じ値を
再指定するか `--reuse-values` を付けてください。

## kagent 統合

kagent 1.x は新しい API（`api.kagent.dev/v1alpha3`）を使い、0.x の API
（`kagent.dev/v1alpha2`）を読みません。逆も同じです。Helm チャートは正しい方を
自分で選び、マニフェストは kagent の系列ごとに分かれています。クラスタがどちらの
系列かは次のように確認します：

```bash
kubectl api-resources --api-group=api.kagent.dev   # agents が出れば kagent 1.x
kubectl api-resources --api-group=kagent.dev       # agents が出れば kagent 0.x
```

kagent 0.x のサポートは、kagent 1.x が正式リリースされた時点で削除します。

### kagent 0.x

次の 2 つはどちらを選んでも結果は同じです。いずれか一方を実行してください。

**A. Helm トグル**：インストール 1 回で、サーバー、kagent への登録
（RemoteMCPServer）、サンプルエージェント `ratatosk-agent` までデプロイ
されます：

```bash
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true
```

`kagent.modelConfig` のデフォルトは `default-model-config` です。環境が
異なる場合は上書きし、サンプルエージェントなしで登録だけしたい場合は
`kagent.agent.enabled=false` を設定してください。

**B. マニフェスト**：同じ 3 つのリソースを、Helm を使わずコピー＆ペースト
で適用します（[詳細](../examples/kagent/0.x/README.ja.md)）：

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml
kubectl apply -f $BASE/0.x/ratatosk-remote-mcpserver.yaml
kubectl apply -f $BASE/0.x/ratatosk-agent.yaml
```

この経路にクローンは不要です。マニフェストには `namespace: kagent` が
入っています。

### kagent 1.x

**まず Harness。** kagent 1.x はすべての Agent を Harness（ランタイム、Agent
Substrate の WorkerPool、スナップショットの保存先）の上で動かしますが、kagent を
インストールしても Harness は作られません。インストール先のネームスペースに
あるか確認してください：

```bash
kubectl get harnesses.api.kagent.dev -n kagent
```

なければ 1 つ作成してください。手順は kagent の
[Agent harness](https://kagent.dev/docs/kagent/1.x/agents/agent-harness/)
にあります。このエージェントには kagent ランタイム（`spec.kagent: {}`）を
使ってください。理由は下の読み取り専用の説明にあります。

**A. Helm トグル**（Harness 名を指定）：

```bash
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true --set kagent.harness=<Harness 名>
```

チャートはクラスタの 1.x API を見て 1.x のオブジェクトを作ります。
`kagent.harness` がなければサーバーと RemoteMCPServer だけをインストールして
エージェントは作らず、インストール後の案内（NOTES）に後から追加する方法
（`helm upgrade --reuse-values --set kagent.harness=<名前>`）が出ます。

**B. マニフェスト**（[詳細](../examples/kagent/1.x/README.ja.md)）：

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml
kubectl apply -f $BASE/1.x/ratatosk-remote-mcpserver.yaml
curl -fsSLO $BASE/1.x/ratatosk-agent.yaml
# spec.harnessRef.name（仮の値 your-harness）を自分の Harness 名に直してから:
kubectl apply -f ratatosk-agent.yaml
```

**Service の完全な名前。** RemoteMCPServer は `ratatosk-mcp` ではなく
`ratatosk-mcp.kagent.svc.cluster.local` を指します。kagent 1.x のエージェントは
egress ポリシーに書かれたホスト名しか解決できないサンドボックスで動くため、
短い Service 名はそこでは解決できません。チャートはリリースのネームスペースと
`clusterDomain`（デフォルト `cluster.local`）からこの名前を作ります。
マニフェストを使う場合、クラスタのドメインが違えば URL を書き換えてください。

#### エージェントのクラスタアクセスは読み取り専用に

kagent のツールサーバー（`kagent-tool-server`）には `k8s_apply_manifest`、
`k8s_delete_resource`、`k8s_scale` などの書き込みツールもあります。サンプル
エージェントは `k8s_get_resources` と `k8s_get_resource_yaml` だけを指定し、
kagent ランタイムはこの一覧を守ります。Claude Harness（`spec.claude`）は
サーバーのツールを一部だけ渡すことができず、ツールサーバーのツールをすべて
エージェントに渡して、Agent のステータスに警告を残すだけです。そうなると
指示文の「クラスタを変更しない」という一文だけが歯止めになります。そこで、
次のどちらかを行ってください：

- このエージェントを kagent ランタイムの Harness で動かす。または
- kagent のインストールでツールサーバーを読み取り専用にする。
  `--set 'kagent-tools.tools.args={--read-only}'` を指定すると、ツールサーバーは
  書き込みツールを登録しません。`kagent-tools.rbac.readOnly=true` はツール
  サーバーの cluster-admin 権限を読み取り専用の権限に替えますが、その権限には
  nodes とカスタムリソースが含まれません。`kagent-tools.rbac.additionalRules`
  で追加しないと、エージェントはノードから読むバージョン（kubelet、コンテナ
  ランタイム）を失います。

ツールサーバーは共有されるため、これらの設定はツールサーバーを使うすべての
エージェントに適用されます。

### 確認

どちらの系列でも、kagent UI に `ratatosk-agent` が現れます。kagent 1.x では
`kubectl describe agents.api.kagent.dev ratatosk-agent -n kagent` で準備状態を
確認できます（リソース名は完全な形で指定してください。残った 0.x の CRD が
`kubectl get agent` に代わりに答えることがあります）。こう聞いてみてください：

> 「このクラスタで、アップグレード前に対応すべきことはありますか？」

エージェントは kagent 内蔵の読み取り専用クラスタツールで、稼働中の
バージョンを自分で調べます（`kagent.agent.k8sTools=false` で無効化
できます）。質問にバージョンを直接書いても構いません。エージェントが UI に
現れない場合は[トラブルシューティング](#トラブルシューティング)を見て
ください。

> **モデルの最低要件**（kagent 0.x で測定）：エージェントを 1 回実行すると、
> 内部でモデル呼び出しが 6 回以上発生します。一方で kagent の Go ADK は、
> 429（リクエスト上限超過）応答をリトライしません。したがって毎分およそ
> 10 リクエスト以上を許容するモデルティアが必要で、それより低いと実行の
> たびに失敗します。たとえば Gemini 無料ティア（2026-07 時点）では、毎分
> 5 リクエストの full-flash 系では実行が完走せず、flash-lite 系だけが
> エージェントを動かせる上限を備えています。

## 設定リファレンス

サーバーの動作を変える設定はすべて環境変数で、Helm チャートは各変数に
対応する値を提供します。チャートにはこのほかに、レプリカ数・リソース・
サービスタイプのようにデプロイの形を決める値もあります。
[チャート README](../charts/ratatosk-mcp/README.ja.md) を参照してください。

| 環境変数 | チャート値 | デフォルト | 説明 |
|---|---|---|---|
| `RATATOSK_URL` | `ratatoskUrl` | `https://ratatosk.io` | アップストリームの API（`/v2`、公開、読み取り専用）。egress をプロキシ・ミラー経由にするとき変更 |
| `MCP_HTTP_ADDR` | *（チャートが `service.port` から自動設定）* | *（未設定 = stdio）* | 設定時（例：`:8080`）は stdio の代わりに `/mcp` の Streamable HTTP で提供、`/healthz` 付き |
| `MCP_HTTP_STATELESS` | `statelessHttp` | *（オフ）* | `1` でセッション状態なしの HTTP：`Mcp-Session-Id` の往復がなく、MCP 仕様 2026-07-28 リビジョンを使う最新クライアントを HTTP で受けるには必須です。レプリカ 2 つ以上への水平スケールにも推奨。旧来のクライアントはどちらでも動きます |
| `MCP_LOG` | `logLevel` | `info` | 許容値は `info`（デフォルト）・`debug`・`warn`・`error` です。認識できない値は警告なしに `info` として扱われます。`debug` はアップストリーム呼び出しごとの所要時間を追加し、`warn`・`error` はログを減らします。どのレベルでもリクエスト引数は記録されません。[ログと監査ストリーム](#ログと監査ストリーム)参照 |
| `MCP_AUDIT` | `auditMode` | *（オフ）* | `metadata` または `full`：ツール呼び出しの監査ストリーム。[監査ストリームを有効にする](#監査ストリームを有効にする)参照 |
| `MCP_RATE_LIMIT_PER_MIN` | `rateLimitPerMin` | *（オフ）* | 呼び出し元ごとの毎分リクエスト上限（MCP のメッセージはすべて数え、ツール呼び出しは 1 回につき 1 つ）。自分が管理していない呼び出し元を受けるサーバー向けです。呼び出し元アドレスごとに枠を分けるため、誰か 1 人が忙しくても他が枯渇しません。超えると `Retry-After` 付きの `429` を返します。アドレスはウィンドウの間だけのメモリ上のバケットキーであり、記録もアップストリームへの転送もしません。`CF-Connecting-IP` → `X-Forwarded-For` → `X-Real-IP` → ピアアドレスの順に読むため、前段のプロキシがいずれかを設定し、**クライアントが送ってきた値は削除する**必要があります。呼び出し元が操作できる値をキーにしたら制限になりません。単一利用者のインストールではオフのままに |
| `MCP_MAX_INFLIGHT` | `maxInflight` | *（オフ）* | すべての呼び出し元から届くリクエストの合計について、サーバーが同時に処理する数の上限。上限を超えたリクエストは、処理枠が空くまで最大 10 秒待ち、空かなければ `Retry-After` 付きの `503` を受け取ります。[メモリと同時実行](#メモリと同時実行)を参照 |
| `MCP_CACHE_MAX_MB` | `cacheMaxMB` | `32` | 応答キャッシュの上限（MiB）。応答は 5 分間キャッシュされます |

docker で HTTP モードを直接起動する例：

```bash
docker run --rm -p 8080:8080 -e MCP_HTTP_ADDR=:8080 ghcr.io/basphere/ratatosk-mcp:1.0.0
```

### メモリと同時実行

まず制約になるのはメモリです。`check_stack` の実行中、サーバーはそのスタックのために読んだリリース文書を
メモリに保持します。コンポーネントが 10 個の構成で測定したところ、`detail` をデフォルトの `brief` のままにすると
約 30 MiB、`detail=full` では約 50 MiB でした。同時に実行される full の呼び出しが 1 つ増えるごとに約 20 MiB 増えます。
さらに、応答キャッシュのメモリ使用量は最大 `MCP_CACHE_MAX_MB` MiB です。

チャートのデフォルトのメモリ上限 512Mi では、スタック全体を full で確認する呼び出しを同時に 16 個（ピーク 376 MiB）
または 24 個（ピーク 495 MiB）受けても、再起動しませんでした。64Mi では、2 個の full 呼び出しを同時に実行すると、
コンテナが強制終了されました。CPU は、コンテナが同時実行に耐えられるかどうかではなく、処理速度に影響します。
チャートのデフォルト 100m では、コンポーネント 10 個の full 確認 1 回に約 4 秒かかりました。同時実行中の呼び出しは
CPU を分け合うため、16 個を同時に実行した場合、所要時間は 46 秒でした。

負荷を抑える設定は 4 つです。

| 設定 | 使う場面 |
|---|---|
| `resources.limits.memory`（チャート） | 呼び出し元からスタック全体の確認リクエストが並列に届く場合や、余裕を増やしたい場合。同時実行する full の呼び出し 1 件あたり、約 20 MiB を見積もってください |
| `MCP_MAX_INFLIGHT` | メモリ上限内で処理できる数を超えるリクエストが、同時に届く可能性がある場合。上限を超えたリクエストは待ったうえで `Retry-After` 付きの `503` を受け取ります。コンテナが強制終了され、処理中のリクエストがすべて失敗する事態を避けるための設定です。メモリ上限で処理できる full の呼び出し数より小さく設定してください |
| `MCP_RATE_LIMIT_PER_MIN` | 自分が管理していない呼び出し元からのアクセスを受けるサーバー。こちらは呼び出し元ごとに制限し、`MCP_MAX_INFLIGHT` は全呼び出し元の合計に対して制限します |
| `MCP_CACHE_MAX_MB` | メモリに余裕がない場合。キャッシュ容量を小さくすると、同じリクエストがアップストリームに送信される回数が増えます |

チャートのデフォルト値で使う単一利用者のインストールなら、これらの設定を変える必要はありません。

## ログと監査ストリーム

### 運用ログ

ログは stderr に JSON 1 行ずつ書かれます（stdout は stdio トランスポート
専用）。デフォルトレベル（`info`）で残るのは、ライフサイクル行（起動時の
`listening` 1 行、stdio セッション終了の 1 行）と、アップストリーム接続が
不調なときの警告・エラー行だけです。正常なリクエストは 1 行も残しません。
なお、stdio セッション終了の行には 2 つの形があります。クライアントが
正常に終了処理を行って切断した場合は `"msg":"stdio session ended"`（INFO）、
終了処理を行わずに切断された場合は
`"msg":"stdio session ended with error"`（ERROR）です。後者も
障害ではなく、終了のしかたの記録です。エラーが起きても元のエラーメッセージは
コピーしません（リクエスト URL に稼働中のバージョンが含まれるためです）。
代わりに、エンドポイントのパターンとエラーの種類だけからログの文言を
作り直して記録します：

```json
{"time":"…","level":"ERROR","msg":"upstream fetch failed","service":"mcp","upstream":"/v2/projects","kind":"connection_refused","tool":"check_stack"}
```

`MCP_LOG=debug` に上げると、これに加えてアップストリーム呼び出しごとに
エンドポイントのパターン・ステータスコード・所要時間が 1 行ずつ付きます。
**どのレベルでも、リクエスト引数（バージョンなど）はログに含まれません。**

### 監査ストリームを有効にする

監査ストリームは「どのクライアントがどのツールを呼んだか」を残す別枠の
記録です。**デフォルトはオフで、オフの間は何も出力されません。**
ストリーム自体が存在しません。

有効にするには `MCP_AUDIT` を設定します：

```bash
# docker
docker run -i --rm -e MCP_AUDIT=metadata ghcr.io/basphere/ratatosk-mcp:1.0.0

# Helm
helm upgrade ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp --set auditMode=metadata
```

有効にすると、ツール呼び出しごとに `event:"audit"` の JSON 1 行が運用ログと
同じストリーム（stderr）に残ります：

```json
{"argument_names":["components","detail"],"client_name":"my-agent","client_version":"1.0.0","event":"audit","level":"INFO","msg":"audit","outcome":"ok","service":"mcp","time":"2026-07-31T02:54:47.029169122Z","tool":"check_stack","transport":"stdio"}
```

サーバーをステートフル（状態保持）HTTP モード（チャートのデフォルト）で
動かしているとき、監査レコードにトランスポートセッション
識別子である `session_id` フィールドが加わります（例：
`"session_id":"T3E77BYZFDDA33SIUSORQ365ZL"`）。同時に接続している
呼び出し元をセッション単位で区別するための値です。`statelessHttp`
（環境変数 `MCP_HTTP_STATELESS`）を有効にすると、特定できるセッションが
存在しないため、`session_id` もクライアントが自己申告した `clientInfo` も
レコードから消え、呼び出し単位をつなぐキーは後述の `trace_id` だけに
なります。監査ストリームで呼び出し元を区別する必要があるなら、ステートフル
モードで動かすか、呼び出し元に `traceparent` を送らせてください。上の例の
ような stdio のレコードにも `session_id` はありません。プロセス 1 つに
呼び出し元は 1 つだけで、区別するものがないためです。

2 つのモードの違い：

- **`metadata`**：ツール名、結果（`ok`・`error`・`tool_error`）、
  トランスポート、クライアントが自己申告した `clientInfo`（stdio・
  ステートフル HTTP のとき）、そして引数の**名前**だけ。引数の値はこの
  モードでは決して記録されません。
- **`full`**：上記に加えて引数の値の全体（`arguments` フィールド）。
  `check_stack` に渡したバージョン一覧もここに入るため、その値がログ
  システムに残ってよいか判断してから有効にしてください。

このストリームは利用者のインフラ内で生成され、自前のログコレクタに
蓄積されます。保持期間と改ざん防止はログプラットフォーム側で決めればよく、
`event` フィールドが `audit` のレコードを別のシンクにルーティングすれば、
運用ログとは別の保持ポリシーを与えられます。

限界も知っておいてください。このサーバーには認証がないため、記録上の
「呼び出し元」は、クライアントが自己申告した `clientInfo` と、トランス
ポート層から得られる情報までです。どのユーザーがエージェントにその呼び出し
を行わせたかは、エージェント層の記録から探す必要があります。

ホスト型エンドポイントに監査ストリームはありません。リクエスト内容を
保持しないという運用原則に従い、常にオフです。監査要件があるなら
セルフホストを使ってください。

### トレースの相関付け（traceparent）

エージェントフレームワークが W3C trace context（分散トレーシング標準の
リクエスト識別ヘッダー）に対応している場合、ツール呼び出しの `_meta` に
`traceparent` を載せて送れます：

```json
{"method":"tools/call","params":{"name":"check_stack","arguments":{"components":[…]},"_meta":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}}
```

すると、その呼び出しが残すログ行（エラー・`debug` レベル）と監査レコードに
`trace_id` フィールドが付与され、アップストリームの `/v2` リクエストにも
同じ `traceparent` ヘッダーが転送されて、エージェント → MCP サーバー →
アップストリームを 1 つの `trace_id` でつなげられます。先に述べた
「監査記録だけでは呼び出し元をたどりきれない」という限界を、層をまたいで
補うための結合キーがこれです。形式が不正な値は破棄されて転送されず、
送らなければ何も記録されません。

## トラブルシューティング

| 症状 | 確認 | 原因と対処 |
|---|---|---|
| ツール呼び出しがエラーを返し（`check_stack` は「could not read the project list: …」。一部の取得だけが失敗した場合はコンポーネントの `note` に `fetch failed: …`）、ログに `"msg":"upstream fetch failed"` と `kind: connection_refused`・`dns`・`timeout` | `kubectl logs deploy/ratatosk-mcp`（ローカルはクライアントの MCP ログ） | egress の遮断。`ratatosk.io:443` へのアウトバウンド HTTPS を許可するか、ミラーを立てて `RATATOSK_URL`（チャート：`ratatoskUrl`）で指定 |
| ログに `"msg":"upstream rate limited"`、`status: 429` | 上と同じ | アップストリーム上限（毎分 1200 リクエスト/IP）の超過。プロジェクトごとの呼び出しを `check_stack` 1 回にまとめ、しばらく待って再試行 |
| HTTP クライアントが `400 Bad Request: protocol version "2026-07-28" is only supported on stateless HTTP servers` を受け取る | クライアントが送る `Mcp-Protocol-Version` ヘッダー | クライアントが MCP 2026-07-28 リビジョンを使っており、ステートフル HTTP モードはこれを拒否します。`--set statelessHttp=true`（環境変数：`MCP_HTTP_STATELESS=1`）を有効にしてください。エラー文の `StreamableHTTPOptions.Stateless` は同じスイッチの Go SDK 側の名前です |
| kagent UI に `ratatosk-agent` が現れない | `kubectl api-resources \| grep kagent` · `kubectl get pods -n kagent` | kagent CRD のないクラスタでは `kagent.enabled=true` のインストールが失敗します。先に kagent をインストールしてください。kagent 1.x で `kagent.harness` が空だとチャートはエージェントを作らず（インストール後の案内に出ます）、指定した Harness が存在しなければ `kubectl describe agents.api.kagent.dev ratatosk-agent` に現れます。0.x のマニフェストを kagent 1.x に適用する（またはその逆）と "no matches for kind" で失敗するので、系列に合うセットを使ってください。マニフェスト経路は `namespace: kagent` がハードコードされているため、kagent を別のネームスペースに入れている場合はマニフェストの修正が必要です |
| kagent 1.x：エージェントの ratatosk ツール呼び出しが接続に失敗する | `kubectl get remotemcpservers.api.kagent.dev ratatosk-mcp -n kagent -o jsonpath='{.spec.url}'` | URL が短い Service 名です。kagent 1.x のエージェントは完全な名前しか解決できません。`http://ratatosk-mcp.<namespace>.svc.cluster.local:8080/mcp` を使ってください（チャートと 1.x のマニフェストはすでにそうしています） |
| kagent 0.x のみ：kagent でエージェントランタイムを Go ADK に変えると `ImagePullBackOff` | `kubectl describe pod` | このチャートとは無関係な、kagent 0.9.12 自体の既知の問題（[#2247]、0.9.12 以降で修正）：Go ADK イメージは `ghcr.io` にのみ公開されているのに、コントローラのデフォルトが廃止済みの `cr.kagent.dev` を指しています。回避策：kagent のインストールに `--set controller.agentImage.registry=ghcr.io` |
| Pod が再起動し、`kubectl describe pod` に `Last State: Terminated, Reason: OOMKilled` が出る | `kubectl describe pod -l app.kubernetes.io/name=ratatosk-mcp` | スタック全体の確認が同時に実行され、メモリ使用量が上限を超えました。`resources.limits.memory` を上げるか、`maxInflight` を設定してあふれた呼び出しを待たせてください。[メモリと同時実行](#メモリと同時実行)を参照 |
| kagent エージェントが実行のたびに失敗 | エージェントログの 429 | モデルティアのリクエスト上限不足です。[kagent 統合](#kagent-統合)のモデル最低要件を参照してください |

[#2247]: https://github.com/kagent-dev/kagent/issues/2247

## バージョン固定

- **コンテナイメージ**：リリースごとにバージョンタグ（例：`1.0.0`）で自動
  ビルドされ、マルチアーキテクチャ（`linux/amd64`、`linux/arm64`）です。
  `latest` は最新リリースを追います。再現可能なデプロイにはバージョンタグを
  固定してください。
- **Helm**：チャートはチャートリポジトリに公開されず、リポジトリの
  クローンからのみインストールします。特定バージョンに固定するには、
  クローン後にリリースタグをチェックアウトしてください：

  ```bash
  git -C ratatosk-mcp checkout v1.0.0
  ```

  イメージタグはデフォルトでチャートの `appVersion` に従い、`image.tag`
  値で上書きできます。

## アップストリーム API

このサーバーは公開 REST API の薄いクライアントです。直接呼びたい場合は、
ratatosk.io の `GET /v2` を呼ぶと API 自身の説明が返ります。API キーは
不要で、IP ごとに毎分 1200 リクエストの制限があります。

## 次のステップ

- 6 つのツールのパラメータ・呼び出し例・実測レスポンス：[ツールリファレンス](tools.ja.md)
- ツール一覧表（早見）：[README のツール表](../README.ja.md#ツール)
- チャート値の全体と kagent トグル：[チャート README](../charts/ratatosk-mcp/README.ja.md)
- kagent のマニフェストとサンプルエージェント：[kagent の例](../examples/kagent/README.ja.md)
