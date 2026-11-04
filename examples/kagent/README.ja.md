# Ratatosk MCP サーバー

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

[Ratatosk](https://ratatosk.io) は CNCF プロジェクトのリリースノートを読み、
変更を 1 件ずつ 7 種類（セキュリティ、破壊的変更、非推奨、重大な修正、機能・改善、
依存関係の更新、その他の変更）に分けます。各変更には、誰が影響を受けるかと、
根拠となったリリースノートの行が付きます。この MCP サーバーがそのリリース情報を
クラスタ内のエージェントに提供します。

プライバシー: `check_stack` は稼働中のバージョンをこのプロセス内で比較するため、
`check_stack` の呼び出しで ratatosk.io に送られるのはプロジェクトのスラッグだけ
です。例外は `check_upgrade` と `get_release` で、指定したバージョンを送ります。
上流 API は公開・読み取り専用・認証不要です（IP あたり毎分 1200 リクエスト制限）。

## ツール

| ツール | 用途 |
| --- | --- |
| `list_projects` | 追跡プロジェクトとそのスラッグ。スラッグはまずここで確認 |
| `check_stack` | 稼働中バージョンをプロジェクトのリリースと照合（ローカル比較）：アップグレード前の確認事項、判断が必要な変更、バージョン番号の飛び |
| `check_upgrade` | 1 プロジェクトの 1 回のアップグレードをサーバーが計算：`from`・`to` がサーバーに送られる |
| `get_release` | リリース 1 件の全体：アップグレード前の確認事項、根拠付きの変更すべて |
| `list_releases` | 最近のリリースの 1 行要約、新しい順 |
| `search` | セキュリティアドバイザリ番号・フラグ・テキストでリリースを探す |

## kagent のバージョンに合うセットを選ぶ

kagent 1.x は新しい API（`api.kagent.dev/v1alpha3`）を使い、0.x の API
（`kagent.dev/v1alpha2`）を読みません。逆も同じなので、kagent の系列ごとに
マニフェストのセットを分けています。クラスタがどちらを提供しているか確認して
ください:

```bash
kubectl api-resources --api-group=api.kagent.dev   # agents が出れば kagent 1.x
kubectl api-resources --api-group=kagent.dev       # agents が出れば kagent 0.x
```

| kagent | マニフェスト | 始める前に必要なもの |
| --- | --- | --- |
| 0.x（0.10.x） | [`0.x/`](0.x/README.ja.md) | kagent のインストール |
| 1.x（1.0 正式リリース前） | [`1.x/`](1.x/README.ja.md) | kagent のインストールと、エージェントを動かす **Harness** |

どちらのセットも、サーバーは共通の [`ratatosk-deploy.yaml`](ratatosk-deploy.yaml)
（Deployment + Service、RBAC 不要）で起動します。kagent 0.x のサポートは、
kagent 1.x が正式リリースされた時点で削除します。

リポジトリの Helm チャートを使うこともできます。チャートが kagent の系列を
自分で判別します（1.x では `--set kagent.harness=<Harness 名>` を追加して
ください）:

```bash
git clone https://github.com/basphere/ratatosk-mcp
helm install ratatosk-mcp ./ratatosk-mcp/charts/ratatosk-mcp -n kagent --set kagent.enabled=true
```

## 聞いてみる

> 「このクラスタでアップグレード前に対応が必要なものはありますか？」

エージェントは kagent 内蔵の読み取り専用クラスタツール
（`kagent-tool-server` の `k8s_get_resources`、`k8s_get_resource_yaml`。
既定の kagent インストールに含まれます）で稼働中のバージョンを自力で
見つけます。バージョンを直接伝えても構いません:

> 「kubernetes v1.36.0、cilium v1.17.18、envoy v1.38.3 を運用中です。
> アップグレード前に対応が必要なものはありますか？」

エージェントは深刻度順に整理した change に、リリースノート原文の引用と
ソースリンクを添えて答えます。
