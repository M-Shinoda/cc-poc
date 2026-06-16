-- pg_partman は専用スキーマが必要。拡張より先に作成する
CREATE SCHEMA IF NOT EXISTS partman;
CREATE EXTENSION IF NOT EXISTS pg_partman SCHEMA partman;
CREATE EXTENSION IF NOT EXISTS pg_cron;

-- strategies
CREATE TABLE IF NOT EXISTS strategies (
    id           SERIAL         PRIMARY KEY,
    name         VARCHAR(100)   NOT NULL UNIQUE,
    config       JSONB          NOT NULL DEFAULT '{}',
    initial_cash NUMERIC(18,2)  NOT NULL,
    created_at   TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

-- portfolios (current state per strategy)
CREATE TABLE IF NOT EXISTS portfolios (
    strategy_id  INTEGER        PRIMARY KEY REFERENCES strategies(id),
    cash_jpy     NUMERIC(18,2)  NOT NULL,
    btc_amount   NUMERIC(18,8)  NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

-- orders
CREATE TABLE IF NOT EXISTS orders (
    id           BIGSERIAL      PRIMARY KEY,
    strategy_id  INTEGER        NOT NULL REFERENCES strategies(id),
    side         VARCHAR(4)     NOT NULL CHECK (side IN ('BUY', 'SELL')),
    status       VARCHAR(10)    NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'filled', 'cancelled')),
    amount       NUMERIC(18,8)  NOT NULL,
    limit_price  NUMERIC(18,2),
    order_price  NUMERIC(18,2)  NOT NULL,
    exec_price   NUMERIC(18,2),
    fee          NUMERIC(18,2),
    created_at   TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    filled_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS orders_strategy_id_idx ON orders (strategy_id);
CREATE INDEX IF NOT EXISTS orders_pending_idx     ON orders (strategy_id) WHERE status = 'pending';

-- price_history (月次レンジパーティション)
CREATE TABLE IF NOT EXISTS price_history (
    id           BIGSERIAL,
    pair         VARCHAR(20)    NOT NULL,
    last         NUMERIC(18,2)  NOT NULL,
    bid          NUMERIC(18,2)  NOT NULL,
    ask          NUMERIC(18,2)  NOT NULL,
    volume       NUMERIC(18,8)  NOT NULL,
    recorded_at  TIMESTAMPTZ    NOT NULL,
    PRIMARY KEY (id, recorded_at)
) PARTITION BY RANGE (recorded_at);

-- pg_partman で月次パーティションを自動生成 (前後 2 ヶ月分をプリメイク)
SELECT partman.create_parent(
    p_parent_table := 'public.price_history',
    p_control      := 'recorded_at',
    p_interval     := '1 month',
    p_premake      := 2
);

-- 保存期間 180 日・期限切れパーティションは自動削除
UPDATE partman.part_config
SET    retention            = '180 days',
       retention_keep_table = false
WHERE  parent_table = 'public.price_history';

-- pg_cron: 毎日 03:00 にパーティション管理 (新規作成 + 期限切れ削除)
SELECT cron.schedule(
    'partman-maintenance',
    '0 3 * * *',
    'SELECT partman.run_maintenance()'
);
