# 仮想通貨シミュレーション取引システム 仕様書

## 1. システム概要

CoinCheck の実際の市場価格を使用し、複数の売買ルール（ストラテジー）を並列実行できる**ペーパートレーディング（模擬取引）システム**。
実際の注文は発行せず、架空のポートフォリオで損益をシミュレートする。

本システムは**常時稼働（24時間365日）を前提**とし、長時間の安定運用・障害からの自動回復・再起動時の状態復元を設計上の基本要件とする。

---

## 2. 目標・スコープ

| 項目 | 内容 |
|------|------|
| 取引所 | CoinCheck のみ |
| 対象通貨 | BTC/JPY |
| 取引モード | シミュレーションのみ（実取引は対象外） |
| 実行環境 | ローカル Linux PC（Docker Compose で管理） |
| 稼働形態 | 常時稼働（24時間365日連続運転） |

---

## 3. 機能要件

### 3.1 価格データ取得

- CoinCheck パブリック API（認証不要）の `GET /api/ticker` をポーリングして価格を取得する
- ポーリング間隔は設定可能（デフォルト: 1秒）
- API 接続断時はエクスポネンシャルバックオフで自動リトライする（上限: 60秒）
- 接続が 5 分以上復旧しない場合はアラートログを出力し、復旧後に自動再開する
- 取得した価格は `price_history` テーブルにすべて記録し、プロセス再起動後も過去データを参照できる

### 3.2 シミュレーション約定エンジン

- 実際の注文は発行しない。取得した市場価格をもとに架空の約定を計算する
- 約定価格の決定ルール:
  - **買い注文**: `Ask × (1 + slippage_rate)`
  - **売り注文**: `Bid × (1 - slippage_rate)`
  - `slippage_rate` のデフォルト: `0.0001`（0.01%）
- 手数料: `0%`（`fee_rate: 0.0`）。CoinCheck は現在 Maker/Taker ともに無料
- 手数料率は将来の料金改定に備えて設定ファイルで変更可能
- 成行注文・指値注文の両方をシミュレート可能
  - 指値注文: 指定価格に到達した時点で約定とみなす

### 3.3 利益確定ガード

- 各ストラテジーは BUY 約定時に**コスト基準**を記録する:
  ```
  コスト基準 = Ask × (1 + slippage_rate) × (1 + fee_rate)
  ```
- SELL シグナルが発生しても、以下の条件を満たさない場合は注文を発行しない:
  ```
  Bid × (1 - slippage_rate) × (1 - fee_rate) > コスト基準
  ```
- コスト基準はプロセス再起動時に DB の最終 BUY 約定価格から復元する

### 3.4 ストラテジー

各ストラテジーは独立した仮想ポートフォリオ（資金・保有量）を持ち、同時並列で動作する。

#### 実装済みストラテジー

| ストラテジー名 | タイプ | ロジック |
|---|---|---|
| `ma_cross_5_20` | `ma_cross` | 短期MA(5) が長期MA(20) を上抜けで BUY、下抜けで SELL |
| `rsi_14` | `rsi` | RSI(14) が 30 未満で BUY、70 超で SELL |

#### ストラテジー共通仕様

- 1 ストラテジーにつき最大 1 ポジションを保持
- 再起動時にポートフォリオ状態・未決注文・価格履歴バッファを DB から自動復元
- ウォームアップ: 起動時に DB から過去 500 件の価格データを読み込み、指標を初期化する

### 3.5 リアルタイム Web ダッシュボード

`http://localhost:8080` でブラウザからリアルタイムに状態を確認できる。

| エンドポイント | 内容 |
|---|---|
| `GET /` | ダッシュボード HTML |
| `GET /api/portfolio` | ポートフォリオ一覧 JSON |
| `GET /api/orders?limit=N` | 約定履歴 JSON（デフォルト 50 件） |
| `GET /events` | SSE ストリーム（価格ティック・約定イベントをリアルタイムにプッシュ） |
| `GET /health` | ヘルスチェック（最終価格取得時刻・稼働時間） |

ダッシュボードの表示内容:
- BTC/JPY 現在価格（価格ティックごとにリアルタイム更新）
- ストラテジーごとの現金・BTC 保有量・BTC 評価額・損益・損益率
- 約定履歴一覧（約定が発生すると自動更新）

### 3.6 コンソールレポート

30 秒ごとに標準出力へポートフォリオサマリーを出力する。

---

## 4. 非機能要件

| 項目 | 要件 |
|------|------|
| 稼働形態 | 常時稼働（24時間365日） |
| 再起動回復 | コンテナ再起動後にストラテジー状態を DB から自動復元 |
| プロセス管理 | Docker Compose の `restart: always` でクラッシュ時に自動再起動 |
| API 障害対応 | エクスポネンシャルバックオフ（上限 60 秒）で自動リトライ |
| ログ | 構造化ログ（JSON Lines）で stdout 出力 |
| 永続化 | 取引履歴・損益・価格データは PostgreSQL コンテナに保存 |

---

## 5. システムアーキテクチャ

### 5.1 サービス構成（Docker Compose）

```
┌──────────────────────────────────────────────────────────┐
│  docker-compose.yml                                      │
│                                                          │
│  ┌─────────────────────┐   ┌──────────────────────────┐  │
│  │  go-core  :8080     │   │  python-analyzer         │  │
│  │  (常時稼働)          │   │  (手動実行)               │  │
│  │                     │   │                          │  │
│  │  価格フィーダー      │   │  損益レポート             │  │
│  │  約定エンジン        │   │  チャート出力             │  │
│  │  ストラテジー実行    │   │                          │  │
│  │  Web ダッシュボード  │   └──────────┬───────────────┘  │
│  └──────────┬──────────┘              │                  │
│             │ SQL                    │ SQL（読み取り）    │
│             ▼                        ▼                   │
│  ┌──────────────────────────────────────────────────┐    │
│  │  postgres                                        │    │
│  │  price_history / strategies / orders / portfolios│    │
│  └──────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────┘
```

### 5.2 go-core 内部アーキテクチャ

```
CoinCheck API
     │ HTTP ポーリング（1秒間隔）
     ▼
PriceFeed (goroutine)
     │ Ticker チャネル
     ▼
main ループ
  ├─ price_history に保存
  ├─ SSE Hub へブロードキャスト（ダッシュボード更新）
  ├─ Executor.CheckPending（指値注文の約定チェック）
  └─ 各 Strategy.OnPrice
         │ OrderRequest（BUY / SELL / nil）
         ▼
     Executor.Execute
       ├─ 利益確定ガードチェック（SELL のみ）
       ├─ 約定価格計算（Ask/Bid + slippage）
       ├─ DB 更新（orders + portfolios トランザクション）
       └─ SSE Hub へ約定イベントをブロードキャスト
```

---

## 6. 技術スタック

### 言語・役割分担

| 言語 | 担当領域 |
|------|---------|
| **Go** | リアルタイムコア（価格取得・戦略実行・約定・DB 書き込み・Web ダッシュボード） |
| **Python** | 分析・レポート・チャート生成 |

### インフラ

| コンポーネント | 技術 |
|--------------|------|
| コンテナ管理 | Docker Compose |
| DB | PostgreSQL（pg_partman・pg_cron 拡張込みカスタムイメージ） |
| ログ | Docker logging driver（json-file） |

### 主要ライブラリ（Go）

| 用途 | ライブラリ |
|------|----------|
| PostgreSQL ドライバ | `jackc/pgx/v5` |
| 設定ファイル | `gopkg.in/yaml.v3` |
| 構造化ログ | `log/slog`（標準ライブラリ） |
| テクニカル指標 | 自前実装（SMA・RSI） |
| Web / SSE | `net/http`（標準ライブラリ） |
| 静的ファイル配信 | `embed`（標準ライブラリ） |

### 主要ライブラリ（Python）

| 用途 | ライブラリ |
|------|----------|
| データ操作 | `pandas` |
| PostgreSQL 接続 | `psycopg2` |
| テーブル表示 | `tabulate` |
| 可視化 | `matplotlib` |

---

## 7. 設定ファイル（config/strategies.yaml）

```yaml
poll_interval: 1s
slippage_rate: 0.0001          # 0.01%（板スプレッド相当）
fee_rate: 0.0                  # 0%（CoinCheck は現在無料）
price_history_retention_days: 180
warmup_bars: 500               # 起動時に DB から読み込む価格履歴件数

strategies:
  - name: ma_cross_5_20
    type: ma_cross
    initial_cash: 1000000      # 100万円
    params:
      short_period: 5
      long_period: 20
      trade_amount: 0.001      # BTC

  - name: rsi_14
    type: rsi
    initial_cash: 1000000
    params:
      period: 14
      oversold: 30
      overbought: 70
      trade_amount: 0.001      # BTC
```

---

## 8. データモデル（PostgreSQL スキーマ）

### `strategies` テーブル

| カラム | 型 | 説明 |
|--------|-----|------|
| id | SERIAL PK | |
| name | VARCHAR(100) | ストラテジー名（UNIQUE） |
| config | JSONB | パラメータ設定 |
| initial_cash | NUMERIC(18,2) | 初期仮想資金（JPY） |
| created_at | TIMESTAMPTZ | |

### `portfolios` テーブル

| カラム | 型 | 説明 |
|--------|-----|------|
| strategy_id | INTEGER PK / FK | |
| cash_jpy | NUMERIC(18,2) | 現在の仮想現金残高 |
| btc_amount | NUMERIC(18,8) | 現在の BTC 保有量 |
| updated_at | TIMESTAMPTZ | |

### `orders` テーブル

| カラム | 型 | 説明 |
|--------|-----|------|
| id | BIGSERIAL PK | |
| strategy_id | INTEGER FK | |
| side | VARCHAR(4) | `BUY` / `SELL` |
| status | VARCHAR(10) | `pending` / `filled` / `cancelled` |
| amount | NUMERIC(18,8) | 発注量（BTC） |
| limit_price | NUMERIC(18,2) | 指値価格（成行の場合は NULL） |
| order_price | NUMERIC(18,2) | 発注時の市場価格 |
| exec_price | NUMERIC(18,2) | 約定価格（スリッページ適用後） |
| fee | NUMERIC(18,2) | 手数料（JPY） |
| created_at | TIMESTAMPTZ | |
| filled_at | TIMESTAMPTZ | 約定日時 |

### `price_history` テーブル（月次レンジパーティション）

| カラム | 型 | 説明 |
|--------|-----|------|
| id | BIGSERIAL | |
| pair | VARCHAR(20) | 通貨ペア（`btc_jpy`） |
| last | NUMERIC(18,2) | 最終取引価格 |
| bid | NUMERIC(18,2) | 買い気配 |
| ask | NUMERIC(18,2) | 売り気配 |
| volume | NUMERIC(18,8) | 出来高 |
| recorded_at | TIMESTAMPTZ | パーティションキー |

- `pg_partman` で月次パーティションを自動生成
- `pg_cron` で毎日 03:00 にパーティション管理（新規作成・期限切れ削除）
- 保存期間: 180 日

---

## 9. ディレクトリ構成

```
cc-poc/
├── docker-compose.yml
├── .env                          # POSTGRES_PASSWORD（git 管理外）
├── config/
│   └── strategies.yaml           # ストラテジー設定
├── go-core/
│   ├── Dockerfile
│   ├── go.mod
│   └── cmd/
│   │   └── main.go               # エントリーポイント・メインループ
│   └── internal/
│       ├── api/
│       │   ├── handler.go        # Web ダッシュボード・REST API・SSE
│       │   └── static/
│       │       └── index.html    # ダッシュボード UI（go:embed で配信）
│       ├── config/
│       │   └── config.go         # YAML 設定読み込み
│       ├── db/
│       │   └── db.go             # PostgreSQL アクセス層
│       ├── engine/
│       │   ├── event.go          # PriceEvent / OrderRequest 型定義
│       │   └── executor.go       # 約定エンジン（利益確定ガード含む）
│       ├── feed/
│       │   └── coincheck.go      # CoinCheck API クライアント
│       ├── health/
│       │   └── handler.go        # ヘルスチェックエンドポイント
│       ├── hub/
│       │   └── hub.go            # SSE クライアント管理・ブロードキャスト
│       ├── reporter/
│       │   └── console.go        # 30 秒間隔コンソールレポート
│       └── strategy/
│           ├── base.go           # Strategy インターフェース・共通関数
│           ├── ma_cross.go       # 移動平均クロス戦略
│           └── rsi.go            # RSI 戦略
├── postgres/
│   ├── Dockerfile                # pg_partman・pg_cron 込みカスタムイメージ
│   └── init.sql                  # スキーマ初期化・パーティション設定
└── python/
    ├── Dockerfile
    ├── requirements.txt
    └── report.py                 # 損益レポート・チャート生成スクリプト
```

---

## 10. 運用コマンド

```bash
# 起動
docker compose up -d

# ダッシュボード
open http://localhost:8080

# ログ確認
docker compose logs -f go-core

# Python レポート出力
docker compose build python-analyzer
docker compose run python-analyzer python report.py

# チャート出力
docker compose run python-analyzer python report.py --chart

# DB リセット（全データ削除）
docker compose down -v
docker compose up -d
```

---

## 11. 今後の拡張ポイント

| 項目 | 内容 |
|------|------|
| 新ストラテジー追加 | `strategy/` に `Strategy` インターフェースを実装し `main.go` に登録するだけ |
| 実取引への移行 | `executor.go` の約定処理を CoinCheck Private API 呼び出しに差し替える |
| バックテスト | `price_history` の過去データを使った検証機能 |
| アラート機能 | 損益が閾値を超えた場合の通知（メール・Slack 等） |
| 複数通貨ペア | ETH/JPY 等への対応 |
| Python シグナルサービス | FastAPI + gRPC で常時稼働させ Go から ML シグナルをリクエスト |
