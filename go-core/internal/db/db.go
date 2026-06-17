package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	pool *pgxpool.Pool
}

type Order struct {
	ID         int64
	StrategyID int64
	Side       string
	Status     string
	Amount     float64
	LimitPrice *float64
	OrderPrice float64
	ExecPrice  *float64
	Fee        *float64
	CreatedAt  time.Time
	FilledAt   *time.Time
}

type Portfolio struct {
	StrategyID int64
	CashJPY    float64
	BTCAmount  float64
	UpdatedAt  time.Time
}

type PortfolioSummary struct {
	StrategyName string
	InitialCash  float64
	CashJPY      float64
	BTCAmount    float64
}

type FilledOrder struct {
	ID           int64
	StrategyName string
	Side         string
	ExecPrice    float64
	Amount       float64
	Fee          float64
	FilledAt     time.Time
}

type PriceRecord struct {
	Pair       string
	Last       float64
	Bid        float64
	Ask        float64
	Volume     float64
	RecordedAt time.Time
}

func New(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return &DB{pool: pool}, nil
}

func (d *DB) Close() {
	d.pool.Close()
}

// SavePrice inserts a ticker snapshot into price_history.
func (d *DB) SavePrice(ctx context.Context, r PriceRecord) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO price_history (pair, last, bid, ask, volume, recorded_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		r.Pair, r.Last, r.Bid, r.Ask, r.Volume, r.RecordedAt)
	return err
}

// GetRecentPrices returns the most recent N records for a pair in chronological order.
func (d *DB) GetRecentPrices(ctx context.Context, pair string, limit int) ([]PriceRecord, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT pair, last, bid, ask, volume, recorded_at
		 FROM price_history
		 WHERE pair = $1
		 ORDER BY recorded_at DESC
		 LIMIT $2`,
		pair, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []PriceRecord
	for rows.Next() {
		var r PriceRecord
		if err := rows.Scan(&r.Pair, &r.Last, &r.Bid, &r.Ask, &r.Volume, &r.RecordedAt); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse to chronological order
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	return records, nil
}

// CreateOrLoadStrategy upserts a strategy row and returns its id.
// On conflict (same name), it updates the config. The portfolio row is
// created with initial_cash only when it does not yet exist.
func (d *DB) CreateOrLoadStrategy(ctx context.Context, name string, initialCash float64, params map[string]interface{}) (int64, error) {
	configJSON, err := json.Marshal(params)
	if err != nil {
		return 0, fmt.Errorf("marshal config: %w", err)
	}

	var id int64
	err = d.pool.QueryRow(ctx,
		`INSERT INTO strategies (name, config, initial_cash)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (name) DO UPDATE SET config = EXCLUDED.config
		 RETURNING id`,
		name, string(configJSON), initialCash).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert strategy: %w", err)
	}

	_, err = d.pool.Exec(ctx,
		`INSERT INTO portfolios (strategy_id, cash_jpy, btc_amount)
		 VALUES ($1, $2, 0)
		 ON CONFLICT (strategy_id) DO NOTHING`,
		id, initialCash)
	if err != nil {
		return 0, fmt.Errorf("init portfolio: %w", err)
	}
	return id, nil
}

// GetPortfolio returns the current portfolio state for a strategy.
func (d *DB) GetPortfolio(ctx context.Context, strategyID int64) (*Portfolio, error) {
	var p Portfolio
	err := d.pool.QueryRow(ctx,
		`SELECT strategy_id, cash_jpy, btc_amount, updated_at
		 FROM portfolios WHERE strategy_id = $1`,
		strategyID).Scan(&p.StrategyID, &p.CashJPY, &p.BTCAmount, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetAllPortfolios returns portfolios joined with strategy names.
func (d *DB) GetAllPortfolios(ctx context.Context) ([]Portfolio, []string, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT p.strategy_id, p.cash_jpy, p.btc_amount, p.updated_at, s.name
		 FROM portfolios p JOIN strategies s ON s.id = p.strategy_id
		 ORDER BY p.strategy_id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var portfolios []Portfolio
	var names []string
	for rows.Next() {
		var p Portfolio
		var name string
		if err := rows.Scan(&p.StrategyID, &p.CashJPY, &p.BTCAmount, &p.UpdatedAt, &name); err != nil {
			return nil, nil, err
		}
		portfolios = append(portfolios, p)
		names = append(names, name)
	}
	return portfolios, names, rows.Err()
}

// GetPendingOrders returns all pending limit orders for a strategy.
func (d *DB) GetPendingOrders(ctx context.Context, strategyID int64) ([]*Order, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT id, strategy_id, side, status, amount, limit_price, order_price, created_at
		 FROM orders WHERE strategy_id = $1 AND status = 'pending'`,
		strategyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.StrategyID, &o.Side, &o.Status,
			&o.Amount, &o.LimitPrice, &o.OrderPrice, &o.CreatedAt); err != nil {
			return nil, err
		}
		orders = append(orders, &o)
	}
	return orders, rows.Err()
}

// GetPortfolioSummaries returns portfolios joined with strategy name and initial_cash.
func (d *DB) GetPortfolioSummaries(ctx context.Context) ([]PortfolioSummary, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT s.name, s.initial_cash, p.cash_jpy, p.btc_amount
		 FROM portfolios p JOIN strategies s ON s.id = p.strategy_id
		 ORDER BY p.strategy_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PortfolioSummary
	for rows.Next() {
		var ps PortfolioSummary
		if err := rows.Scan(&ps.StrategyName, &ps.InitialCash, &ps.CashJPY, &ps.BTCAmount); err != nil {
			return nil, err
		}
		result = append(result, ps)
	}
	return result, rows.Err()
}

// GetFilledOrders returns the most recent filled orders across all strategies.
func (d *DB) GetFilledOrders(ctx context.Context, limit int) ([]FilledOrder, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT o.id, s.name, o.side, o.exec_price, o.amount, COALESCE(o.fee, 0), o.filled_at
		 FROM orders o JOIN strategies s ON s.id = o.strategy_id
		 WHERE o.status = 'filled'
		 ORDER BY o.filled_at DESC
		 LIMIT $1`,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []FilledOrder
	for rows.Next() {
		var fo FilledOrder
		if err := rows.Scan(&fo.ID, &fo.StrategyName, &fo.Side,
			&fo.ExecPrice, &fo.Amount, &fo.Fee, &fo.FilledAt); err != nil {
			return nil, err
		}
		result = append(result, fo)
	}
	return result, rows.Err()
}

// GetLastBuyPrice returns the exec_price of the most recent filled BUY order.
// Returns 0, nil when no filled BUY order exists yet.
func (d *DB) GetLastBuyPrice(ctx context.Context, strategyID int64) (float64, error) {
	var price float64
	err := d.pool.QueryRow(ctx,
		`SELECT exec_price FROM orders
		 WHERE strategy_id = $1 AND side = 'BUY' AND status = 'filled'
		 ORDER BY filled_at DESC LIMIT 1`,
		strategyID).Scan(&price)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return price, err
}

// CountFilledOrders returns the number of filled orders for a strategy.
func (d *DB) CountFilledOrders(ctx context.Context, strategyID int64) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM orders WHERE strategy_id = $1 AND status = 'filled'`,
		strategyID).Scan(&n)
	return n, err
}

// FillOrder inserts a new filled market order and updates the portfolio atomically.
func (d *DB) FillOrder(ctx context.Context, o *Order) error {
	return d.runTx(ctx, func(tx pgx.Tx) error {
		var cashJPY, btcAmount float64
		err := tx.QueryRow(ctx,
			`SELECT cash_jpy, btc_amount FROM portfolios
			 WHERE strategy_id = $1 FOR UPDATE`,
			o.StrategyID).Scan(&cashJPY, &btcAmount)
		if err != nil {
			return fmt.Errorf("lock portfolio: %w", err)
		}

		execPrice := *o.ExecPrice
		fee := *o.Fee
		execCost := execPrice * o.Amount
		var newCash, newBTC float64
		switch o.Side {
		case "BUY":
			total := execCost + fee
			if cashJPY < total {
				return fmt.Errorf("insufficient cash (have %.2f, need %.2f)", cashJPY, total)
			}
			newCash = cashJPY - total
			newBTC = btcAmount + o.Amount
		case "SELL":
			if btcAmount < o.Amount {
				return fmt.Errorf("insufficient BTC (have %.8f, need %.8f)", btcAmount, o.Amount)
			}
			newCash = cashJPY + execCost - fee
			newBTC = btcAmount - o.Amount
		default:
			return fmt.Errorf("unknown side %q", o.Side)
		}

		err = tx.QueryRow(ctx,
			`INSERT INTO orders
			   (strategy_id, side, status, amount, order_price, exec_price, fee, filled_at)
			 VALUES ($1, $2, 'filled', $3, $4, $5, $6, $7)
			 RETURNING id`,
			o.StrategyID, o.Side, o.Amount,
			o.OrderPrice, o.ExecPrice, o.Fee, o.FilledAt).Scan(&o.ID)
		if err != nil {
			return fmt.Errorf("insert order: %w", err)
		}

		_, err = tx.Exec(ctx,
			`UPDATE portfolios SET cash_jpy=$1, btc_amount=$2, updated_at=NOW()
			 WHERE strategy_id=$3`,
			newCash, newBTC, o.StrategyID)
		return err
	})
}

// InsertPendingOrder inserts a limit order with status='pending'.
func (d *DB) InsertPendingOrder(ctx context.Context, o *Order) error {
	return d.pool.QueryRow(ctx,
		`INSERT INTO orders (strategy_id, side, status, amount, limit_price, order_price)
		 VALUES ($1, $2, 'pending', $3, $4, $5)
		 RETURNING id`,
		o.StrategyID, o.Side, o.Amount, o.LimitPrice, o.OrderPrice).Scan(&o.ID)
}

// FillPendingOrder fills an existing pending limit order and updates the portfolio atomically.
func (d *DB) FillPendingOrder(ctx context.Context, orderID int64, execPrice, fee float64, filledAt time.Time) error {
	return d.runTx(ctx, func(tx pgx.Tx) error {
		var strategyID int64
		var side string
		var amount float64
		err := tx.QueryRow(ctx,
			`UPDATE orders
			 SET status='filled', exec_price=$1, fee=$2, filled_at=$3
			 WHERE id=$4 AND status='pending'
			 RETURNING strategy_id, side, amount`,
			execPrice, fee, filledAt, orderID).Scan(&strategyID, &side, &amount)
		if err != nil {
			return fmt.Errorf("update order: %w", err)
		}

		var cashJPY, btcAmount float64
		err = tx.QueryRow(ctx,
			`SELECT cash_jpy, btc_amount FROM portfolios
			 WHERE strategy_id=$1 FOR UPDATE`, strategyID).Scan(&cashJPY, &btcAmount)
		if err != nil {
			return fmt.Errorf("lock portfolio: %w", err)
		}

		execCost := execPrice * amount
		var newCash, newBTC float64
		switch side {
		case "BUY":
			newCash = cashJPY - execCost - fee
			newBTC = btcAmount + amount
		case "SELL":
			newCash = cashJPY + execCost - fee
			newBTC = btcAmount - amount
		}

		_, err = tx.Exec(ctx,
			`UPDATE portfolios SET cash_jpy=$1, btc_amount=$2, updated_at=NOW()
			 WHERE strategy_id=$3`,
			newCash, newBTC, strategyID)
		return err
	})
}

func (d *DB) runTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
