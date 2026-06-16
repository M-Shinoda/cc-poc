# CoinCheck Paper Trading Simulator

CoinCheck の取引所（板取引）レートを使って、複数の売買ルールを並列でシミュレートするシステムです。
実際の注文は発行しません。

---

## 前提条件

| ソフトウェア | バージョン |
|-------------|-----------|
| Docker Engine | 24以上 |
| Docker Compose | v2.20以上（`docker compose` コマンド） |
| インターネット接続 | CoinCheck API へのアクセスに必要 |

---

## 初回セットアップ

### 1. 環境変数ファイルを作成

```bash
cp .env.example .env
```

`.env` を開いてパスワードを変更します。

```
POSTGRES_PASSWORD=任意の強いパスワード
```

### 2. ビルド

```bash
docker compose build
```

> 初回は PostgreSQL の拡張（pg_partman / pg_cron）のインストールがあるため数分かかります。

### 3. 起動

```bash
docker compose up -d
```

起動順序は Docker が自動管理します。

```
postgres（起動 + ヘルスチェック通過）
    ↓
go-core（価格取得・シミュレーション開始）
```

---

## 動作確認

### ログをリアルタイムで確認

```bash
# go-core のログ（価格取得・約定ログ）
docker compose logs -f go-core

# PostgreSQL のログ
docker compose logs -f postgres
```

正常起動時の go-core ログ例：

```json
{"level":"INFO","msg":"strategy loaded","name":"ma_cross_5_20","type":"ma_cross","id":1}
{"level":"INFO","msg":"strategy loaded","name":"rsi_14","type":"rsi","id":2}
{"level":"INFO","msg":"simulator started","strategies":2,"poll_interval":"1s"}
```

### ヘルスチェック

```bash
curl http://localhost:8080/health
```

```json
{
  "last_fetch_ago": 0.5,
  "status": "ok",
  "uptime_seconds": 42.1
}
```

| フィールド | 説明 |
|-----------|------|
| `status` | `ok`（正常）/ `stale`（30秒以上価格取得なし） |
| `last_fetch_ago` | 最終価格取得からの経過秒数 |
| `uptime_seconds` | 起動からの経過秒数 |

### コンテナの稼働状況

```bash
docker compose ps
```

---

## 損益レポートの確認

### テキストサマリー

```bash
docker compose run python-analyzer python report.py
```

出力例：

```
════════════════════════════════════════════════════════
  CoinCheck Paper Trader  BTC/JPY: ¥15,234,567
════════════════════════════════════════════════════════
╭──────────────┬──────────────┬────────────┬─────────╮
│ Strategy     │ Cash (JPY)   │ Total(JPY) │ P&L     │
├──────────────┼──────────────┼────────────┼─────────┤
│ ma_cross_5_20│ ¥987,654     │ ¥1,002,876 │ +¥2,876 │
│ rsi_14       │ ¥1,000,000   │ ¥1,000,000 │ ¥0      │
╰──────────────┴──────────────┴────────────┴─────────╯
```

### チャート付きレポート（PNG出力）

```bash
mkdir -p output
docker compose run -v $(pwd)/output:/output python-analyzer python report.py --chart
```

`output/chart.png` に価格チャートと約定ポイントが出力されます。

### 取得する価格履歴の時間範囲を指定

```bash
# 直近 48 時間分のデータを対象にする（デフォルトは 24 時間）
docker compose run python-analyzer python report.py --hours 48
```

---

## 設定変更

### ストラテジーのパラメータを変更する

[config/strategies.yaml](config/strategies.yaml) を編集してから go-core を再起動します。

```bash
# 設定を編集後
docker compose restart go-core
```

### 主要設定値

```yaml
poll_interval: 1s        # 価格取得間隔
slippage_rate: 0.0001    # スリッページ率（取引所スプレッド相当）
fee_rate: 0.0            # 手数料率（CoinCheck 取引所は現在 0%）
warmup_bars: 500         # 起動時に DB から読み込む価格履歴件数
```

---

## 通常運用コマンド

```bash
# 停止（データは保持）
docker compose stop

# 再起動
docker compose restart

# 完全削除（データも消える）
docker compose down -v

# go-core だけ再起動（設定変更後など）
docker compose restart go-core
```

---

## データベースに直接アクセスする

```bash
docker compose exec postgres psql -U ccpoc -d ccpoc
```

よく使うクエリ：

```sql
-- 現在のポートフォリオ一覧
SELECT s.name, p.cash_jpy, p.btc_amount, p.updated_at
FROM portfolios p JOIN strategies s ON s.id = p.strategy_id;

-- 直近の約定履歴
SELECT s.name, o.side, o.amount, o.exec_price, o.fee, o.filled_at
FROM orders o JOIN strategies s ON s.id = o.strategy_id
WHERE o.status = 'filled'
ORDER BY o.filled_at DESC
LIMIT 20;

-- 価格履歴の件数確認
SELECT COUNT(*), MIN(recorded_at), MAX(recorded_at)
FROM price_history;

-- パーティション一覧
SELECT inhrelid::regclass AS partition_name
FROM pg_inherits
WHERE inhparent = 'price_history'::regclass;
```

---

## ディレクトリ構成

```
cc-poc/
├── docker-compose.yml
├── .env                    # 環境変数（git 管理外）
├── .env.example            # .env のテンプレート
├── config/
│   └── strategies.yaml     # ストラテジー設定（ここを編集）
├── go-core/                # Go コアサービス（常時稼働）
│   ├── Dockerfile
│   ├── go.mod
│   ├── cmd/main.go
│   └── internal/
│       ├── config/         # 設定読み込み
│       ├── db/             # PostgreSQL 操作
│       ├── engine/         # 約定エンジン
│       ├── feed/           # CoinCheck API クライアント
│       ├── health/         # ヘルスチェック HTTP
│       ├── reporter/       # コンソール表示
│       └── strategy/       # 売買ルール実装
├── postgres/               # カスタム PostgreSQL イメージ
│   ├── Dockerfile          # pg_partman / pg_cron を追加
│   └── init.sql            # スキーマ・パーティション初期化
├── python/                 # 分析スクリプト（手動実行）
│   ├── Dockerfile
│   ├── requirements.txt
│   └── report.py
└── spec.md                 # システム仕様書
```

---

## トラブルシューティング

### go-core が起動しない

postgres の初期化が完了していない可能性があります。

```bash
docker compose logs postgres
```

`database system is ready to accept connections` が出るまで待ってから再確認します。

```bash
docker compose restart go-core
```

### 価格が取得できない（status: stale）

CoinCheck API に接続できていない可能性があります。

```bash
# コンテナ内から直接確認
docker compose exec go-core wget -qO- https://coincheck.com/api/ticker
```

### PostgreSQL の拡張が見つからないエラー

`postgres/Dockerfile` のビルドが古いキャッシュを使っている場合があります。

```bash
docker compose build --no-cache postgres
docker compose up -d
```
