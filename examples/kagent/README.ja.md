# Ratatosk MCP サーバー

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

[Ratatosk](https://ratatosk.io) は CNCF の卒業済みまたはインキュベーション中の
プロジェクトのリリースノートを読み、変更を 1 件ずつ 7 種類（セキュリティ・
互換性の変更・非推奨・重要な修正・機能・改善・依存関係の更新・その他）に分け
ます。各変更には誰が影響を受けるかと、根拠となったリリースノートの行が付きます。
この MCP サーバーがそのリリース情報をクラスタ内のエージェントに提供します。

プライバシー: `check_stack` ツールは稼働中のバージョンをこのプロセス内で
ローカルに比較します。ratatosk サーバーに送られるのはプロジェクトのスラッグ
だけで、バージョンがクラスタの外に出ることはありません。上流 API は公開・
読み取り専用・認証不要です（IP あたり毎分 1200 リクエスト制限）。

## ツール

| ツール | 用途 |
| --- | --- |
| `list_projects` | 追跡プロジェクトの一覧 — スラッグはまずここで確認 |
| `check_stack` | 稼働中バージョンをプロジェクトのリリースと照合（ローカル比較）：アップグレード前の確認事項、判断が必要な変更、バージョン番号の飛び |
| `check_upgrade` | 1 プロジェクトの 1 回のアップグレードをサーバーが計算 — `from`・`to` がサーバーに送られる |
| `get_release` | リリース 1 件の全体：アップグレード前の確認事項、根拠付きの変更すべて |
| `list_releases` | 最近のリリースの 1 行要約、新しい順 |
| `search` | セキュリティアドバイザリ番号・フラグ・テキストでリリースを探す |

## インストール

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml            # Deployment + Service（RBAC 不要）
kubectl apply -f $BASE/ratatosk-remote-mcpserver.yaml  # kagent への登録
kubectl apply -f $BASE/ratatosk-agent.yaml             # 任意: サンプルエージェント ratatosk-agent
```

マニフェストには `namespace: kagent` が入っています。このディレクトリからローカル
ファイルを適用しても同じです。

Helm を使う場合は、チャートの kagent トグル 1 つで同じ構成になります:

```bash
helm install ratatosk-mcp ../../charts/ratatosk-mcp -n kagent --set kagent.enabled=true
```

サンプルエージェントにこう聞いてみてください:

> 「このクラスタでアップグレード前に対応が必要なものはありますか？」

エージェントは kagent 内蔵の読み取り専用クラスタツール
（`kagent-tool-server` の `k8s_get_resources`、`k8s_get_resource_yaml` —
既定の kagent インストールに含まれます）で稼働中のバージョンを自力で
見つけます。バージョンを直接伝えても構いません:

> 「kubernetes v1.36.0、cilium v1.17.18、envoy v1.38.3 を運用中です。
> アップグレード前に対応が必要なものはありますか？」

エージェントは深刻度順に整理した change に、リリースノート原文の引用と
ソースリンクを添えて答えます。
