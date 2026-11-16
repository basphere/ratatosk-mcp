# kagent 0.x で Ratatosk を使う

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

このマニフェストは kagent 0.x（0.10.x、API `kagent.dev/v1alpha2`）用です。
kagent 1.x では [`../1.x/`](../1.x/README.ja.md) を使ってください。サーバーと
エージェントの役割は [`../README.ja.md`](../README.ja.md) にあります。kagent 0.x
のサポートは、kagent 1.x が正式リリースされた時点で削除します。

## インストール

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml                # Deployment + Service（RBAC 不要）
kubectl apply -f $BASE/0.x/ratatosk-remote-mcpserver.yaml  # kagent への登録
kubectl apply -f $BASE/0.x/ratatosk-agent.yaml             # 任意: サンプルエージェント ratatosk-agent
```

マニフェストには `namespace: kagent` が入っています。クローンしてローカル
ファイルを適用しても同じです。kagent が別のネームスペースにある場合は、3 つの
ファイルの `namespace:` と、RemoteMCPServer の URL に含まれるネームスペースを
一緒に変えてください。

RemoteMCPServer の URL は Service の完全な名前
`ratatosk-mcp.kagent.svc.cluster.local` です。クラスタの DNS ドメインが
`cluster.local` でない場合は書き換えてください。

## 確認

kagent UI に `ratatosk-agent` が現れます。エージェントは kagent 内蔵の
読み取り専用クラスタツール（`kagent-tool-server` の `k8s_get_resources`、
`k8s_get_resource_yaml` — 既定の kagent インストールに含まれます）で稼働中の
バージョンを自力で見つけます。
