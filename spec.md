# 仮想通貨シミュレーション取引システム 仕様書（たたき台）

## 1. システム概要

CoinCheck の実際の市場価格を使用し、複数の売買ルール（ストラテジー）を並列実行できる**ペーパートレーディング（模擬取引）システム**。
実際の注文は発行せず、架空のポートフォリオで損益をシミュレートする。

本システムは**常時稼働（24時間365日）を前提**とし、長時間の安定運用・障害からの自動回復・再起動時の状態復元を設計上の基本要件とする。

---

## 2. 目標・スコープ

| 項目 | 内容 |
|------|------|
| 取引所 | CoinCheck のみ |
| 対象通貨 | BTC/JPY（将来的に拡張可） |
| 取引モード | シミュレーションのみ（実取引は対象外） |
| 実行環境 | ローカル Linux PC（Docker Compose で管理） |
| 稼働形態 | 常時稼働（24時間365日連続運転） |
| 実運用フェーズ | 将来の実自動売買への移行を前提とした設計 |

---

## 3. 機能要件

### 3.1 価格データ取得

- CoinCheck パブリック API（認証不要）からリアルタイム価格を取得する
- 取得対象エンドポイント：
  - `GET /api/ticker` — 最新ティッカー（最終取引価格・売買気配・出来高）
  - `GET /api/trades` — 最新成立取引履歴
  - `GET /api/order_books` — 板（オーダーブック）情報
- ポーリング間隔は設定可能（デフォルト: 1秒）
- 将来的な WebSocket 対応を考慮した抽象化レイヤーを設ける
- API 接続断時はエクスポネンシャルバックオフで自動リトライする（最大待機: 60秒）
- 接続が一定時間（例: 5分）復旧しない場合はアラートログを出力し、復旧後に自動再開する
- 取得した価格は `price_history` テーブルにすべて記録し、プロセス再起動後も過去データを参照できる

### 3.2 シミュレーション約定エンジン

- 実際の注文は発行しない。取得した市場価格を元に架空の約定を計算する
- 取引所（板取引）を前提とした約定価格モデルを採用する
- 約定価格の決定ルール：
  - **買い注文**: `ask価格 × (1 + スリッページ率)`
  - **売り注文**: `bid価格 × (1 - スリッページ率)`
  - スリッページ率はデフォルト 0.01%（取引所の板スプレッド相当）
- 手数料: **0%**（CoinCheck 取引所は現在 Maker/Taker ともに無料）
- 手数料率は将来の料金改定に備えて設定ファイルで変更可能とする
- 成行注文・指値注文の両方をシミュレート可能とする
  - 指値注文: 指定価格に到達した時点で約定とみなす

### 3.3 ストラテジーフレームワーク

- 各ストラテジーは共通インターフェースを実装したプラグイン構造とする
- 複数のストラテジーを**同時並列**で独立実行できる
- 各ストラテジーは独立した仮想ポートフォリオ（資金・保有量）を持つ
- 各ストラテジーが受け取る情報（シグナル入力）：
  - 現在価格（Last / Bid / Ask）
  - タイムスタンプ
  - 過去の価格履歴（ローリングバッファ）
- 各ストラテジーが発行できる命令：
  - `BUY <amount>`
  - `SELL <amount>`
  - `HOLD`（何もしない）

#### 実装予定のストラテジー例（初期）

| ストラテジー名 | ロジック概要 |
|----------------|--------------|
| 単純移動平均クロス | 短期MA > 長期MA で買い、逆で売り |
| RSI | RSI < 30 で買い、RSI > 70 で売り |
| ブレイクアウト | 一定期間の高値/安値を抜けたらエントリー |

### 3.4 損益管理・レポーティング

- 各ストラテジーごとに以下を記録・表示する：
  - 現在の保有量（BTC）
  - 現在の仮想現金残高（JPY）
  - 評価残高（現金 + 保有量の時価評価）
  - 実現損益（確定済み）
  - 含み損益（未確定）
  - 総損益（実現 + 含み）
  - 勝率・平均利益・平均損失
  - 取引回数
- ストラテジー間のパフォーマンス比較表示
- 取引ログの CSV/JSON 出力

---

## 4. 非機能要件

| 項目 | 要件 |
|------|------|
| 稼働形態 | 常時稼働（24時間365日）を前提とした設計 |
| 並列実行 | ストラテジーは独立した goroutine で動作 |
| 信頼性 | API 接続エラー時は自動リトライし、シミュレーションを継続 |
| 再起動回復 | コンテナ再起動後にストラテジーの状態を DB から自動復元 |
| プロセス管理 | Docker Compose の `restart: always` でクラッシュ時に自動再起動 |
| 拡張性 | 新ストラテジーの追加が容易なプラグイン構造 |
| 設定管理 | YAML で設定ファイルを管理（環境変数で上書き可） |
| ログ | 構造化ログ（JSON Lines）で stdout 出力 → Docker logging driver が収集 |
| 永続化 | 取引履歴・損益・価格データは PostgreSQL コンテナに保存 |
| 死活監視 | ヘルスチェックエンドポイント（最終価格取得時刻・稼働時間）を提供 |

### 4.1 常時稼働のための障害対応方針

```
障害種別                  対応方針
───────────────────────────────────────────────────────────────
API 一時エラー           エクスポネンシャルバックオフでリトライ（上限 60 秒）
API 長時間断（5分超）    ログにアラート出力、復旧後に自動再開
コンテナクラッシュ       Docker が自動再起動（restart: always）、DB から状態を復元
OS 再起動               Docker の自動起動設定（--restart always）で対応
DB 接続エラー           リトライ後に失敗したらコンテナ自体を再起動
PostgreSQL 障害         postgres コンテナが再起動、go-core は接続リトライで自動復旧
```

### 4.2 状態復元の設計

コンテナ再起動後も以下を復元できるよう、常に PostgreSQL を正とする：

- 各ストラテジーの仮想現金残高・保有量
- 未決済のシミュレーション注文
- 価格履歴バッファ（テクニカル指標の計算に必要な期間分）

---

## 5. システムアーキテクチャ

### 5.1 サービス構成（Docker Compose）

```
┌─────────────────────────────────────────────────────────────────┐
│  docker-compose.yml                                             │
│                                                                 │
│  ┌──────────────────────┐    ┌──────────────────────────────┐  │
│  │   go-core            │    │   python-analyzer            │  │
│  │   (常時稼働)          │    │   (手動実行 or 常時稼働)      │  │
│  │                      │    │                              │  │
│  │  価格フィーダー       │    │  pandas / matplotlib         │  │
│  │  イベントバス         │    │  レポート生成                 │  │
│  │  戦略実行エンジン     │    │  将来: ML シグナルサービス    │  │
│  │  約定エンジン         │    │                              │  │
│  │  ポートフォリオ管理   │    └──────────────┬───────────────┘  │
│  └──────────┬───────────┘                   │                  │
│             │ SQL                           │ SQL (読み取り)    │
│             ▼                               ▼                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │   postgres                                               │  │
│  │   (常時稼働)                                             │  │
│  │   price_history / strategies / orders / portfolios       │  │
│  └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

### 5.2 go-core 内部アーキテクチャ

```
  CoinCheck API
       │ HTTP ポーリング（1秒間隔）
       ▼
  PriceFeed (goroutine)
       │ 価格イベント
       ▼
  EventBus (channel ベース pub/sub)
    │        │        │
    ▼        ▼        ▼
  [S1]     [S2]     [S3]    ← 各ストラテジーが goroutine で並列動作
    │        │        │
    └────────┴────────┘
             │ 注文イベント
             ▼
      約定エンジン
      （ask/bid + スリッページ + 手数料を適用）
             │
             ▼
      PortfolioManager ──→ PostgreSQL
             │
             ▼
         Reporter（コンソール表示）
```

---

## 6. 技術スタック

### 言語・役割分担

| 言語 | 担当領域 | 理由 |
|------|---------|------|
| **Go** | リアルタイムコア（価格取得・戦略実行・約定・DB書き込み） | goroutine による真の並列処理、長期安定稼働、低レイテンシ |
| **Python** | 分析・レポート・将来の ML シグナル生成 | pandas / matplotlib / scikit-learn の豊富なエコシステム |

### インフラ

| コンポーネント | 技術 | 備考 |
|--------------|------|------|
| コンテナ管理 | Docker Compose | ローカル Linux PC 上で全サービスを一元管理 |
| DB | PostgreSQL 16 | 複数コンテナからの同時アクセスに対応、Docker 公式イメージ使用 |
| ログ収集 | Docker logging driver（json-file） | コンテナの stdout を自動収集、logrotate 相当の設定も可能 |

### 主要ライブラリ（Go / コア）

| 用途 | ライブラリ |
|------|----------|
| HTTP クライアント | `net/http`（標準） |
| PostgreSQL ドライバ | `jackc/pgx` |
| SQL クエリビルダ | `sqlc`（SQL からコード生成） |
| 設定ファイル | `spf13/viper` |
| 構造化ログ | `uber-go/zap` |
| テクニカル指標 | `markcheno/go-talib` または自前実装 |
| テスト | 標準 `testing` + `testify` |

### 主要ライブラリ（Python / 分析）

| 用途 | ライブラリ |
|------|----------|
| データ操作 | `pandas` |
| PostgreSQL 接続 | `psycopg2` または `asyncpg` |
| テクニカル指標 | `pandas-ta` |
| 可視化 | `matplotlib` / `plotly` |
| ML（将来） | `scikit-learn` / `lightgbm` |
| gRPC（将来・パターンB移行時） | `grpcio` |

---

## 7. Docker Compose 構成

```yaml
# docker-compose.yml（概略）

services:
  postgres:
    build: ./postgres          # pg_partman / pg_cron を含むカスタムイメージ
    restart: always
    environment:
      POSTGRES_DB: ccpoc
      POSTGRES_USER: ccpoc
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./postgres/init.sql:/docker-entrypoint-initdb.d/init.sql:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ccpoc"]
      interval: 10s
      retries: 5

  go-core:
    build: ./go-core
    restart: always
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://ccpoc:${POSTGRES_PASSWORD}@postgres:5432/ccpoc
    volumes:
      - ./config:/config:ro

  python-analyzer:
    build: ./python
    profiles: ["tools"]          # 通常は起動しない。手動実行時のみ
    depends_on:
      - postgres
    environment:
      DATABASE_URL: postgres://ccpoc:${POSTGRES_PASSWORD}@postgres:5432/ccpoc

volumes:
  postgres_data:
```

- 通常運用: `docker compose up -d` で go-core + postgres が起動
- 分析実行: `docker compose run python-analyzer python report.py`
- ログ確認: `docker compose logs -f go-core`

---

## 8. データモデル（PostgreSQL スキーマ）

### `strategies` テーブル

| カラム | 型 | 説明 |
|--------|-----|------|
| id | SERIAL PK | |
| name | VARCHAR(100) | ストラテジー名 |
| config | JSONB | パラメータ設定 |
| initial_cash | NUMERIC(18,2) | 初期仮想資金（JPY） |
| created_at | TIMESTAMPTZ | |

### `portfolios` テーブル（ストラテジーの現在状態）

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
| side | VARCHAR(4) | 'BUY' / 'SELL' |
| status | VARCHAR(10) | 'pending' / 'filled' / 'cancelled' |
| amount | NUMERIC(18,8) | 発注量（BTC） |
| limit_price | NUMERIC(18,2) | 指値価格（成行の場合は NULL） |
| order_price | NUMERIC(18,2) | 発注時の市場価格（JPY） |
| exec_price | NUMERIC(18,2) | 約定価格（スリッページ適用後、約定前は NULL） |
| fee | NUMERIC(18,2) | 手数料（JPY、約定前は NULL） |
| created_at | TIMESTAMPTZ | |
| filled_at | TIMESTAMPTZ | 約定日時（約定前は NULL） |

### `price_history` テーブル（月次レンジパーティション）

| カラム | 型 | 説明 |
|--------|-----|------|
| id | BIGSERIAL | |
| pair | VARCHAR(20) | 通貨ペア（例: 'btc_jpy'） |
| last | NUMERIC(18,2) | 最終取引価格 |
| bid | NUMERIC(18,2) | 買い気配 |
| ask | NUMERIC(18,2) | 売り気配 |
| volume | NUMERIC(18,8) | 出来高 |
| recorded_at | TIMESTAMPTZ | パーティションキー |

`recorded_at` による月次レンジパーティションを採用する。パーティションテーブルでは PRIMARY KEY にパーティションキーを含める必要があるため、`(id, recorded_at)` を複合主キーとする。

```sql
CREATE TABLE price_history (
    id          BIGSERIAL,
    pair        VARCHAR(20)    NOT NULL,
    last        NUMERIC(18,2)  NOT NULL,
    bid         NUMERIC(18,2)  NOT NULL,
    ask         NUMERIC(18,2)  NOT NULL,
    volume      NUMERIC(18,8)  NOT NULL,
    recorded_at TIMESTAMPTZ    NOT NULL,
    PRIMARY KEY (id, recorded_at)
) PARTITION BY RANGE (recorded_at);

-- 月ごとの子テーブル例
CREATE TABLE price_history_2026_06
    PARTITION OF price_history
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
```

**パーティション管理方針**

- 子テーブルの作成: `pg_partman` 拡張で自動生成する
- 保存期間: 設定ファイルで指定（デフォルト 180 日）
- 古いパーティションの削除: `pg_cron` で毎日深夜に期限切れの子テーブルを `DROP TABLE` する

`DELETE` による行単位削除と異なり、`DROP TABLE` はファイル削除に相当するため、数億行あっても即座に完了しテーブルロックも発生しない。

**ディスク使用量の見通し（180日保持・1秒ポーリングの場合）**

```
約 90 byte/行 × 86,400 行/日 × 180 日 ≒ 1.35 GB
インデックス・PostgreSQL 管理領域込みで約 2〜2.5 GB
```

**インデックス**

各子テーブルに `recorded_at` の BRIN インデックスを付与する（時系列の範囲検索に最適）。

---

## 9. ディレクトリ構成（案）

```
cc-poc/
├── docker-compose.yml
├── .env                         # POSTGRES_PASSWORD 等（git 管理外）
├── config/
│   └── strategies.yaml          # ストラテジー設定
├── go-core/
│   ├── Dockerfile
│   ├── cmd/
│   │   └── main.go              # エントリーポイント
│   └── internal/
│       ├── feed/
│       │   └── coincheck.go     # CoinCheck API クライアント
│       ├── engine/
│       │   ├── bus.go           # イベントバス
│       │   ├── executor.go      # 約定エンジン
│       │   └── portfolio.go     # ポートフォリオ管理
│       ├── strategy/
│       │   ├── base.go          # ストラテジーインターフェース
│       │   ├── ma_cross.go      # 移動平均クロス
│       │   └── rsi.go           # RSI 戦略
│       ├── db/
│       │   ├── query.sql        # sqlc 用 SQL
│       │   └── models.go        # sqlc 生成コード
│       └── health/
│           └── handler.go       # ヘルスチェック HTTP エンドポイント
├── postgres/
│   ├── Dockerfile               # postgres:16 + pg_partman + pg_cron をインストール
│   └── init.sql                 # pg_partman・pg_cron の有効化・初期パーティション作成
├── python/
│   ├── Dockerfile
│   ├── requirements.txt
│   └── report.py                # 分析・レポート生成スクリプト
└── spec.md
```

---

## 10. 今後の拡張ポイント

### 実取引への移行
- `executor.go` の約定エンジンを実 API 呼び出しに差し替えるだけで実取引に対応できる設計にする
- CoinCheck Private API（要 API キー）連携を後フェーズで追加

### Python シグナルサービス化（パターン B への移行）
- Python を FastAPI + gRPC サーバーとして常時稼働させ、Go から ML シグナルをリクエストする
- `docker-compose.yml` に `python-signal` サービスを追加し、`profiles: ["tools"]` を外すだけで移行できる構造にする

### その他
- バックテスト機能（過去データを使った検証）
- Webダッシュボード化（Grafana + PostgreSQL データソース、または Streamlit）

---

## 11. 未決定事項・検討課題

| 項目 | 検討内容 |
|------|---------|
| 約定価格の算出方法 | Last価格 vs Bid/Ask スプレッド中値 vs 板情報ベース |
| ストラテジーの状態管理 | 再起動時の復元をトランザクションでどこまで保証するか |
| バックテスト | CoinCheck の履歴データ or 外部データソース（Kaiko 等）を使うか |
| アラート機能 | 損益が閾値を超えた場合の通知（メール・Slack 等） |
| 複数通貨ペア | ETH/JPY 等への対応優先度 |
| price_history の保存期間 | デフォルト 180 日。戦略の最長ルックバック期間に合わせて調整 |
