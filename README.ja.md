<div align="center">

<img width="1280" height="625" alt="luycloud 管理画面プレビュー" src="README-preview.jpg" />

# ☁️ luycloud

**オープンソースで軽量な、オールインワン KVM 仮想マシン管理コンソール**

KVM/QEMU と深く統合し、仮想マシンのライフサイクル、ネットワークとストレージのオーケストレーション、スナップショットのクローン、ファイアウォール、帯域幅管理を提供するプライベートクラウドプラットフォームです。

<br/>

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![GitHub Stars](https://img.shields.io/github/stars/luolu1/luycloud?style=social)](https://github.com/luolu1/luycloud)
[![GitHub Forks](https://img.shields.io/github/forks/luolu1/luycloud?style=social)](https://github.com/luolu1/luycloud)
[![GitHub Issues](https://img.shields.io/github/issues/luolu1/luycloud)](https://github.com/luolu1/luycloud/issues)
[![GitHub Pull Requests](https://img.shields.io/github/issues-pr/luolu1/luycloud)](https://github.com/luolu1/luycloud/pulls)

<br/>

[English](README.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md) · 日本語

[**🚀 クイックデプロイ**](#-クイックデプロイ) · [**✨ 主な機能**](#-主な機能) · [**🧰 技術スタック**](#-技術スタック) · [**🤝 貢献ガイド**](#-貢献ガイド) · [**💬 問題を報告**](https://github.com/luolu1/luycloud/issues)

</div>

---

## 📖 概要

luycloud は中小企業や個人のプライベートクラウド向けのオープンソース仮想マシン管理プラットフォームです。KVM/QEMU 仮想化と深く統合し、仮想マシンのライフサイクル管理、ネットワークとストレージのオーケストレーション、スナップショットとクローン、ファイアウォールと帯域幅管理、Web コンソールと API を一体化した完全なソリューションを提供します。

> 💡 luycloud は QVMConsole を基に開発され、元版の一部制限を取り除き、柔軟な拡張とプライベートな導入のために独立したブランドとデプロイ方式を採用しています。

### コアバリュー

| | 価値 | 説明 |
|:---:|:---|:---|
| 🎯 | **運用の敷居を下げる** | すぐに使える仮想化管理プラットフォームを提供し、重複実装のコストを削減 |
| ⚡ | **クリックで使えるテンプレート** | Linux/Windows/OpenWrt などのテンプレートを用意し、低レベルの KVM コマンドを知らなくても数項目の入力で数分以内に VM を作成できます。ディスク形式、ブート方式、ネットワーク設定などは自動処理されます |
| 🧩 | **モジュール設計** | Open vSwitch などのプラグ可能なネットワークバックエンドで、多様なトポロジーとセキュリティポリシーに対応 |
| 🔀 | **二つの入口** | Web コンソールと RESTful API により、自動化と効率的な手動運用の両方に対応 |
| 📊 | **可観測性** | タスクキューと SSE により、時間のかかる処理を可視化・中断可能にし、高い並行性での安定性を向上 |

---

## 🚀 クイックデプロイ

luycloud は依存関係（libvirt / qemu-kvm / Open vSwitch など）のインストール、ユーザーストレージの設定、systemd サービスの登録と起動を自動で行うワンクリックインストールスクリプトを提供します。

### 方法 1：ワンクリックリモートデプロイ（推奨）

ソースを手動でダウンロードする必要はありません。スクリプトが最新ソースをクローンし、Go/Node ツールチェーンを準備してローカルでビルドし、対話式インストールを開始します。

```bash
# root で実行（いずれか一つ）
bash <(curl -fsSL https://raw.githubusercontent.com/luolu1/luycloud/main/luycloud-deploy.sh)
bash <(wget -qO- https://raw.githubusercontent.com/luolu1/luycloud/main/luycloud-deploy.sh)
```

ブートストラップスクリプトは、CPU アーキテクチャの検出 → git、Go、Node.js ツールチェーンの検出・自動インストール → `main` ブランチのクローンまたは更新 → `build.sh` によるフロントエンドとバックエンドのローカルビルド → `install.sh` による対話式インストールを順に実行します。インストール済みの場合は「更新 / アンインストール」メニューに切り替わります。

- 🌐 アクセス URL: `http://<server-IP>:8080`
- 🔑 デフォルトアカウント: `admin` / `admin123`（初回ログイン後すぐに変更してください）

> 📌 ソースを直接クローンしてビルドするため、リポジトリの `main` ブランチと常に同期されます。次の環境変数で動作を上書きできます：`LUYCLOUD_GIT`（リポジトリ URL）、`LUYCLOUD_BRANCH`（ブランチ）、`LUYCLOUD_SRC`（ソースのチェックアウト先、デフォルト `/opt/luycloud-src`）、`LUYCLOUD_VARIANT`（`native` ネイティブ版 / `compat` zig 互換版、デフォルト `native`）、`GOPROXY`（中国向け高速化）、`LUYCLOUD_MIRROR`（`https://ghfast.top` などの GitHub CDN プロキシプレフィックス）。

### ⚡ ワンクリック更新（インストール済みユーザー向け推奨）

インストール済み環境を新しいバージョン（例えば「ごみ箱」機能を追加した版）へ更新する際、再ビルドは不要です。スクリプトが GitHub Releases から**コンパイル済みリリースパッケージ**を取得し、バイナリとフロントエンドを置き換えてサービスを再起動します。

```bash
# root で実行（いずれか一つ）
bash <(curl -fsSL https://raw.githubusercontent.com/luolu1/luycloud/main/update.sh)
bash <(wget -qO- https://raw.githubusercontent.com/luolu1/luycloud/main/update.sh)
```

- 🚀 Go / Node ツールチェーン不要で数秒で更新。GLIBC / AVX2 に応じてネイティブ版または互換版バイナリを自動選択
- 🛡️ 更新前に旧版を自動バックアップし、新版の起動に失敗した場合は自動ロールバック
- 🌏 **CDN 高速化**：中国から GitHub へのアクセスが遅い場合、スクリプト内蔵のプロキシミラーメニューを利用でき、環境変数でも指定できます：

  ```bash
   # CDN プロキシプレフィックスでダウンロードを高速化
  LUYCLOUD_MIRROR="https://ghfast.top" bash <(curl -fsSL https://raw.githubusercontent.com/luolu1/luycloud/main/update.sh)

   # 指定バージョンへ更新（デフォルトは latest）
  LUYCLOUD_RELEASE_TAG="v1.1.0" bash update.sh
  ```

> 💡 `update.sh` は既存インストールの**更新**専用です。初回インストールには上記の `luycloud-deploy.sh` を使用してください。`luycloud-deploy.sh` も `LUYCLOUD_MIRROR` プロキシプレフィックスに対応し、`git clone` を高速化できます。

<details>
<summary><b>方法 2：リリースパッケージからのワンクリックデプロイ</b></summary>

<br/>

ビルド済みのリリースパッケージがある場合：

```bash
# 1. リリースパッケージをダウンロードして展開（amd64 / arm64）
tar -xzf luycloud-linux-amd64.tar.gz
cd luycloud-linux-amd64

# 2. root でワンクリックインストールスクリプトを実行
sudo bash install.sh
```

スクリプトは、ハードウェア仮想化の検出 → 依存関係のインストール → ユーザーストレージの初期化 → ネットワーク基盤（OVS/DHCP/フォワーディング）→ systemd サービスの登録 → サービス起動を順に実行します。インストール完了後、端末にアクセス URL が表示されます。

> インストールスクリプトは対話式です。案内に従ってストレージディスク、容量、Web ポート、パブリックアクセスの有効化、互換性ハードウェアテストの実行を選択してください。

</details>

<details>
<summary><b>方法 3：ソースから手動でビルド</b></summary>

<br/>

Go 1.27+ と Node.js 22+ が必要です（Vite 8 / rolldown には Node ≥ 20.19 が必要です）。

```bash
# フロントエンドとバックエンドをビルドしてリリースパッケージを生成
bash build.sh --variant native      # ホスト上でネイティブビルド
# または
bash build.sh                       # zig 互換版もビルド（zig が必要）

# 生成物：release/luycloud-linux-<arch>.tar.gz
# 展開後、sudo bash install.sh を実行してデプロイ
```

中国のネットワークでのビルドヒント：Go 依存関係には `GOPROXY=https://goproxy.cn,direct` を設定してください。フロントエンド依存関係では、rolldown のネイティブバインディングを正しくインストールするため Node ≥ 20.19 が必要です。

</details>

### 開発モード

```bash
bash start-dev.sh
# バックエンド air ホットリロード（:8080）、フロントエンド Vite（:5173）
```

### よく使う運用コマンド

```bash
systemctl status kvm-console      # サービス状態を表示
journalctl -u kvm-console -f      # リアルタイムログを表示
systemctl restart kvm-console     # サービスを再起動
sudo bash qvmc-manage.sh          # アカウントとセキュリティ管理（パスワードリセット、2FA 解除、ポート変更、パブリックアクセスなど）
```

<details>
<summary><b>⚙️ ネスト仮想化環境でのデプロイに関する注意</b></summary>

<br/>

ホスト自体が仮想マシン内で動作している場合（ネスト KVM）、`host-passthrough` により QEMU が `MSR 0x345 (IA32_PERF_CAPABILITIES)` の設定を試みてクラッシュすることがあります：

```
qemu-system-x86_64: error: failed to set MSR 0x345 to 0x2000
kvm_buf_set_msrs: Assertion `ret == cpu->kvm_msr_buf->nmsrs' failed.
```

luycloud はネスト仮想化（`/proc/cpuinfo` の `hypervisor` フラグ）を検出すると、VM の domain XML に `<pmu state='off'/>` を自動注入し、vPMU を無効にしてこのクラッシュを回避します。ベアメタルホストでは何も変更せず、通常のパフォーマンスカウンター機能にも影響しません。

</details>

---

## ✨ 主な機能

<table>
<tr>
<td width="50%" valign="top">

### 🖥️ 仮想マシンのライフサイクル管理
- 完全な電源操作（起動/シャットダウン/再起動/強制電源オフ/リセット）
- クォータ制御と権限チェック
- メンテナンスモードと正常シャットダウン

### 🌐 ネットワーク仮想化
- VPC 論理スイッチとセキュリティグループ
- ポートフォワーディングと静的 IP 管理
- ファイアウォールポリシー（VM/ホストの二層）
- ネットワーク診断とパケットキャプチャツール

### 💾 ストレージ管理
- ホストストレージプール管理（フォーマット/パーティション/LVM ボリューム）
- テンプレート管理（作成/インポート/エクスポート/削除）
- ディスク管理と IOPS 制限
- ユーザー ISO のマウント

</td>
<td width="50%" valign="top">

### 👥 ユーザー権限とクォータ
- マルチテナント対応（エラスティッククラウド/ライトクラウド）
- きめ細かなクォータ管理（CPU/メモリ/ディスク/VM 数/ストレージ/帯域幅/トラフィック/パブリック IP/ポートフォワーディング/スナップショット）
- SSH アクセス制御と招待登録フロー

### 📈 監視とタスクスケジューリング
- VM/ホストの統計と履歴データ
- 非同期タスクキューとリアルタイム SSE プッシュ
- スケジュールイベントセンターとリソース回収

### 📸 スナップショットバックアップ
- スナップショットの作成/復元/削除/一括削除
- NVRAM と共有ディレクトリの互換性チェック
- クォータ検証とタスク追跡

</td>
</tr>
</table>

<details>
<summary><b>🧬 テンプレートから VM を作成（クリックして展開）</b></summary>

<br/>

- **テンプレート管理**：実行中の VM からワンクリックでテンプレートを作成し、テンプレートパッケージ（tar.gz）のインポート/エクスポート、インポート整合性チェックのプレビューに対応
- **複数のテンプレートタイプ**：Linux（cloud-init）、Windows（ConfigDrive）、OpenWrt（UCI 設定注入）、FnOS（virt-customize）、「初期化しない」モードに対応
- **統一クローンアーキテクチャ**：完全クローンとリンククローンに対応。完全クローンは独立したディスクイメージを作成し、リンククローンは backing chain で高速デプロイを実現
- **システム初期化制御**：システム初期化を無効にしてテンプレート本来の設定を保持し、起動後コマンドのブロッキング/ノンブロッキング実行に対応
- **スマートブート検出**：UEFI/BIOS ブートタイプを自動検出し、NVRAM パスをコピーしてアーキテクチャ間の互換性を確保
- **OpenWrt デュアルモード初期化**：ext4 ルートパーティションと squashfs+overlay のディスクレイアウトを自動検出し、virt-customize または guestfish を適切に選択してネットワーク設定を注入
- **Windows ConfigDrive**：OpenStack 標準準拠の ISO イメージで、cloudbase-init によりホスト名、パスワードなどの初期設定を自動化
- **メタデータ駆動**：テンプレートタイプ、分類、デフォルトハードウェア設定、ハッシュ検証、テンプレートファミリー関係などを `.meta.json` メタデータファイルで管理
- **バージョンと整合性チェック**：MD5 + SHA256 の二重ハッシュ検証でテンプレートディスクの完全性を保証
- **テンプレートファミリー管理**：テンプレートの親子関係、ノードツリー、連鎖削除、サイレント昇格、ホット昇格に対応

</details>

---

## 🧰 技術スタック

<table>
<tr>
<td valign="top" width="50%">

**バックエンド**

| コンポーネント | 選択 |
|:---|:---|
| 言語 | Go 1.27+ |
| Web フレームワーク | Gin v1.12.0 |
| データベース | SQLite + GORM v1.31.1 |
| 仮想化 | go-libvirt RPC |
| 認証 | JWT v5.3.1 + TOTP v1.5.0 + crypto |
| WebSocket | gorilla/websocket v1.5.3 |
| ログ | lumberjack v2.2.1 |

</td>
<td valign="top" width="50%">

**フロントエンド**

| コンポーネント | 選択 |
|:---|:---|
| UI フレームワーク | React v19.2.7 + TypeScript v6.0.2 |
| コンポーネントライブラリ | Semi Design v2.101.1 |
| ビルドツール | Vite v8.1.1 |
| ルーティング | react-router-dom v7.18.1 |
| 状態管理 | Zustand v5.0.14 |
| HTTP クライアント | Axios v1.18.1 |
| グラフ / ターミナル / VNC | ECharts v6.1.0 · @xterm/xterm v6.0.0 · @novnc/novnc v1.7.0 |

</td>
</tr>
</table>

> 旧フロントエンド（バックアップ）：Vue 3.5.30 + Element Plus。`web-backup/` にあります。

**仮想化インフラ**：KVM/QEMU · Open vSwitch · Windows 初期化（ConfigDrive 標準対応）

---

## 📋 システム要件

<table>
<tr>
<td valign="top" width="50%">

**ハードウェア要件**

- ✅ VT-x/AMD-V 対応 CPU
- 🧠 4GB 以上の RAM（8GB 以上を推奨）
- 💽 50GB 以上の空きディスク容量

</td>
<td valign="top" width="50%">

**ソフトウェア要件**

- 🐧 オペレーティングシステム：Debian/Ubuntu（Debian 12 以上を推奨）
- 🖧 仮想化：KVM/QEMU
- 🌉 ネットワーク：Open vSwitch
- 🛠️ 依存ツール：genisoimage（Windows VM の初期化に使用）

</td>
</tr>
</table>

---

## 🤝 貢献ガイド

独立開発者が保守する大規模なオープンソースプロジェクトとして、luycloud は継続的な改善のためにコミュニティの支援を必要としています。AI などのツールを使った修正や開発を歓迎・推奨しますが、次のガイドラインを必ず守ってください。

1. **ルールの遵守**：AI ツールを使用する場合、リポジトリルートの `AGENTS.md` を中核となるプロンプト規則として使用してください
2. **汎用的なシナリオ**：提出する機能は一般的なユースケースを対象とし、幅広いユーザーのニーズに適合させてください。特定シナリオ向けのカスタマイズは、リポジトリを fork して独自に保守することを推奨します

### 🔒 セキュリティ脆弱性の報告

プロジェクトにセキュリティ脆弱性を発見した場合は、深刻度にかかわらず、悪用を防ぐため GitHub Issues で公開報告しないでください。責任ある開示のため、リポジトリの非公開窓口からメンテナーに連絡してください：[非公開のセキュリティフィードバックを送信](https://github.com/luolu1/luycloud/security)。

---

## 🔄 上流変更の取り込み

標準リポジトリのバックエンドに変更がある場合は、詳細なマージ手順について [`docs/merge-from-upstream.md`](docs/merge-from-upstream.md) を参照してください。

**基本原則：**

1. `server/` ディレクトリのバックエンド変更のみをマージする
2. `web/` ディレクトリのフロントエンド変更はすべて拒否する
3. このリポジトリ独自の `web-backup/`、`.gitignore`、`docs/` の内容は上流によって上書きされない

---

## 💖 謝辞

luycloud に貢献してくださったすべての開発者に感謝します！

[**ForZTN**](https://sponsorship.forztn.com/github.com/luolu1/luycloud) にはテストマシンをご支援いただき、特に感謝します。

---

<div align="center">

**☁️ luycloud** — 仮想化管理をより簡単に

[公式サイト](https://github.com/luolu1/luycloud) · [ドキュメント](https://github.com/luolu1/luycloud) · [デプロイガイド](https://github.com/luolu1/luycloud)

<sub>このプロジェクトがお役に立った場合は、⭐ Star で応援してください！</sub>

</div>
