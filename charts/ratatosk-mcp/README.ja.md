# ratatosk-mcp Helm チャート

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

ratatosk MCP サーバーを Streamable HTTP モードの ClusterIP Service として
デプロイします。RBAC もシークレットも不要で、Pod Security Standards
（PSS）の `restricted` プロファイルでも警告なしに動作します。

```bash
# リポジトリのルートで実行
helm install ratatosk-mcp ./charts/ratatosk-mcp
# → MCP エンドポイント: http://ratatosk-mcp.<namespace>.svc:8080/mcp
```

## values の設定項目

| キー | デフォルト値 | 意味 |
| --- | --- | --- |
| `replicaCount` | `1` | レプリカ数 — 2 以上にする前に `statelessHttp` を有効化してください |
| `image.repository` | `ghcr.io/basphere/ratatosk-mcp` | イメージ |
| `image.tag` | チャートの `appVersion` | 特定バージョンの固定 |
| `ratatoskUrl` | `https://ratatosk.io` | アップストリームの changes API — egress をプロキシ・ミラー経由にする場合は変更 |
| `statelessHttp` | `false` | セッション状態なしの HTTP 配信 — MCP 仕様 2026-07-28 リビジョンのクライアントと複数レプリカに必要 |
| `logLevel` | `""` (= info) | `MCP_LOG`: `debug`・`warn`・`error` も受け付け、それ以外の値は警告なしに info 扱い。どのレベルでもリクエスト引数は記録されません |
| `auditMode` | `""` (= 無効) | `MCP_AUDIT`: `metadata` はツール呼び出しごとに 1 行（引数名のみ）、`full` は引数値も記録 |
| `service.port` | `8080` | Service / MCP エンドポイントのポート |
| `resources` | 小さめの requests/limits | 必要に応じて調整 |
| `nodeSelector` / `tolerations` / `affinity` | `{}` / `[]` / `{}` | 標準的なスケジューリング制御 |
| `clusterDomain` | `cluster.local` | kagent の RemoteMCPServer に登録する Service の完全な名前に使うクラスタ DNS ドメイン |
| `kagent.enabled` | `false` | kagent 統合も一緒にインストール（下記） |
| `kagent.modelConfig` | `default-model-config` | kagent 側の ModelConfig 名 |
| `kagent.harness` | `""` | kagent 1.x のみ：サンプルエージェントを動かす Harness。1.x で空ならエージェントを作らない |
| `kagent.agent.enabled` | `true` | サンプルエージェントのデプロイ（kagent.enabled 時） |
| `kagent.agent.name` | `ratatosk-agent` | サンプルエージェント名 |
| `kagent.agent.k8sTools` | `true` | kagent 内蔵の読み取り専用クラスタツールをエージェントに付与し、稼働バージョンを自力で把握 |
| `kagent.agent.toolServer.name` | `kagent-tool-server` | kagent ツールサーバーの RemoteMCPServer 名（`<kagent リリース>-tool-server`） |
| `kagent.agent.toolServer.namespace` | `""` | ツールサーバーがこのリリースと別のネームスペースにある場合、そのネームスペース |

## kagent 統合

```bash
# kagent 0.x
helm install ratatosk-mcp ./charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true
# kagent 1.x：エージェントを動かす Harness 名を指定
helm install ratatosk-mcp ./charts/ratatosk-mcp \
  --namespace kagent --set kagent.enabled=true --set kagent.harness=<名前>
```

`RemoteMCPServer`（kagent がサーバーのツールをすべて自動検出）と、準備済みの
`ratatosk-agent` が追加されます。エージェントには kagent 内蔵の
読み取り専用クラスタツール（`k8s_get_resources`、`k8s_get_resource_yaml`）
も付き、稼働中のバージョンを自力で見つけます。無効にするには
`kagent.agent.k8sTools=false` を指定します。kagent CRD のあるクラスタで
のみ有効化してください。

チャートはクラスタが提供する kagent API に合わせて描画します。kagent 1.x の
API（`api.kagent.dev/v1alpha3`）があれば 1.x の形、なければ kagent 0.x
（`kagent.dev/v1alpha2`）の形です。`helm template` はクラスタを見ないので、
1.x の形が必要なら `--api-versions api.kagent.dev/v1alpha3` を渡してください。
kagent 0.x のサポートは、kagent 1.x が正式リリースされた時点で削除します。

kagent 1.x では：

- すべての Agent は Harness の上で動き、kagent は Harness を作りません。
  `kagent.harness` にこのネームスペースの Harness を指定するまで、チャートは
  サーバーと RemoteMCPServer だけをインストールし、エージェントは作りません。
  エージェントを追加する方法はインストール後の案内（NOTES）に出ます。
- RemoteMCPServer の URL は Service の完全な名前
  （`<service>.<namespace>.svc.<clusterDomain>`）です。kagent 1.x の
  エージェントは短い名前を解決できないためです。クラスタのドメインが
  `cluster.local` でなければ `clusterDomain` を指定してください。
- エージェントのクラスタアクセスは読み取り専用に保ってください。上のツール
  一覧は kagent ランタイムでは守られますが、Claude Harness は kagent
  ツールサーバーのツールを書き込みツールまですべて公開します。kagent
  ランタイムの Harness を使うか、ツールサーバーを読み取り専用にしてください。
  [インストールガイド](../../docs/install.ja.md#kagent-1x)を参照してください。

Helm を使わない場合は [`examples/kagent/`](../../examples/kagent/README.ja.md)
を参照してください。

## アップグレード

```bash
helm upgrade ratatosk-mcp ./charts/ratatosk-mcp
```

`--set`・`-f` で指定した値は自動では引き継がれません — 同じ値を再指定するか
`--reuse-values` を付けてください。`image.tag` で固定しない限りイメージは
チャートの `appVersion` に従います。
