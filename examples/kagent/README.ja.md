# kagent との連携例

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

このディレクトリには、ratatosk-mcp サーバーをクラスタにデプロイし、kagent に MCP
サーバー（`RemoteMCPServer`）として登録したうえで、サンプルエージェント `ratatosk-agent`
を作成するためのマニフェストが含まれています。kagent 0.x 用と kagent 1.x 用の
マニフェストがそれぞれ用意されています。Ratatosk とこのサーバーの概要については、
リポジトリの [README](../../README.ja.md) を参照してください。

`check_stack` は稼働中のバージョンをサーバープロセス内で比較します。そのため、
`check_stack` の呼び出しで ratatosk.io に送信されるのは、プロジェクトのスラッグだけです。
`check_upgrade` と `get_release` は、指定したバージョンを ratatosk.io に送信します。
アカウントや API キーは不要です。また、マニフェストにシークレットは含まれていません。
詳しくは[実行中のコンポーネントバージョンの取り扱い](../../README.ja.md#実行中のコンポーネントバージョンの取り扱い)を
参照してください。

エージェントが使用する 6 つのツールのパラメーターとレスポンス例は、
[ツールリファレンス](../../docs/tools.ja.md)で確認できます。

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
