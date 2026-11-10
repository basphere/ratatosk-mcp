# kagent 1.x で Ratatosk を使う

[English](README.md) · [한국어](README.ko.md) · [日本語](README.ja.md)

このマニフェストは kagent 1.x（1.0 正式リリース前、API `api.kagent.dev/v1alpha3`）
用です。kagent 0.x では [`../0.x/`](../0.x/README.ja.md) を使ってください。
サーバーとエージェントの役割は [`../README.ja.md`](../README.ja.md) にあります。

## 始める前に: Harness

kagent 1.x はすべての Agent を **Harness** の上で動かします。Harness は、どの
ランタイムがエージェントを実行するか、どの Agent Substrate WorkerPool で動くか、
スナップショットをどこに置くかを決めます。kagent をインストールしても Harness は
1 つも作られず、サンプルエージェントは Harness 名を参照するので、まず確認して
ください:

```bash
kubectl get harnesses.api.kagent.dev -n kagent
```

一覧が空なら 1 つ作成してください。手順は kagent の
[Agent harness](https://kagent.dev/docs/kagent/1.x/agents/agent-harness/) に
あります。このエージェントには kagent ランタイムを使ってください:

```yaml
apiVersion: api.kagent.dev/v1alpha3
kind: Harness
metadata:
  name: kagent
  namespace: kagent
spec:
  kagent: {}
  substrate:
    workerPoolRef:
      name: kagent-default        # `kagent install` が用意する WorkerPool
    snapshotPolicy:
      location: s3://<bucket>/kagent/
```

kagent ランタイムを勧める理由: マニフェストに書いた読み取り専用のクラスタ
ツール 2 つだけをエージェントに渡します。Claude Harness（`spec.claude`）は
ツール一覧を守れず、kagent ツールサーバーのツールをすべて公開します。
`k8s_apply_manifest` や `k8s_delete_resource` などの書き込みツールも含まれます。
それでも Claude Harness を使う場合は、先にツールサーバーを読み取り専用にして
ください。[インストールガイド](../../../docs/install.ja.md#kagent-1x)の
読み取り専用の説明を参照してください。

## インストール

```bash
BASE=https://raw.githubusercontent.com/basphere/ratatosk-mcp/main/examples/kagent
kubectl apply -f $BASE/ratatosk-deploy.yaml                # Deployment + Service（RBAC 不要）
kubectl apply -f $BASE/1.x/ratatosk-remote-mcpserver.yaml  # kagent への登録
curl -fsSLO $BASE/1.x/ratatosk-agent.yaml                  # 任意: サンプルエージェント ratatosk-agent
# spec.harnessRef.name（仮の値 your-harness）を自分の Harness 名に直してから:
kubectl apply -f ratatosk-agent.yaml
```

マニフェストには `namespace: kagent` が入っており、Harness は Agent と同じ
ネームスペースにある必要があります。kagent が別のネームスペースにある場合は、
3 つのファイルの `namespace:` と、RemoteMCPServer の URL に含まれる
ネームスペースを一緒に変えてください。

RemoteMCPServer の URL が Service の完全な名前
`ratatosk-mcp.kagent.svc.cluster.local` になっているのは意図的です。kagent 1.x の
エージェントは、egress ポリシーに書かれたホスト名しか解決できないサンドボックス
で動くため、`ratatosk-mcp` のような短い名前はそこでは解決できません。クラスタの
DNS ドメインが `cluster.local` でない場合は書き換えてください。

## 確認

```bash
kubectl describe agents.api.kagent.dev ratatosk-agent -n kagent
```

リソース名は完全な形で指定してください。クラスタに kagent 0.x の CRD が
残っていると、`kubectl get agent` がそちらから答えることがあります。Harness 名が
間違っていれば Agent の conditions に現れます。エージェントを変更すると新しい
リビジョンが作られるので、変更を使うには新しいセッションを開いてください。
