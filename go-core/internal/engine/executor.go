package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"cc-poc/internal/db"
	"cc-poc/internal/hub"
)

type Executor struct {
	db           *db.DB
	slippageRate float64
	feeRate      float64
	pending      map[int64][]*db.Order // keyed by strategy ID
	hub          *hub.Hub
	names        map[int64]string
}

func NewExecutor(database *db.DB, slippageRate, feeRate float64, h *hub.Hub) *Executor {
	return &Executor{
		db:           database,
		slippageRate: slippageRate,
		feeRate:      feeRate,
		pending:      make(map[int64][]*db.Order),
		hub:          h,
		names:        make(map[int64]string),
	}
}

func (e *Executor) RegisterStrategy(id int64, name string) {
	e.names[id] = name
}

func (e *Executor) broadcastFill(strategyID int64, side string, execPrice, amount float64) {
	if e.hub == nil {
		return
	}
	msg, err := json.Marshal(map[string]any{
		"type": "fill", "strategy": e.names[strategyID],
		"side": side, "exec_price": execPrice, "amount": amount,
	})
	if err == nil {
		e.hub.Broadcast(msg)
	}
}

// LoadPending loads pending limit orders from DB for all given strategy IDs.
func (e *Executor) LoadPending(ctx context.Context, strategyIDs []int64) error {
	for _, id := range strategyIDs {
		orders, err := e.db.GetPendingOrders(ctx, id)
		if err != nil {
			return err
		}
		e.pending[id] = orders
	}
	return nil
}

// Execute processes an order request from a strategy.
func (e *Executor) Execute(ctx context.Context, strategyID int64, req *OrderRequest, event PriceEvent) {
	if req.Type == "limit" {
		e.insertPending(ctx, strategyID, req, event)
		return
	}
	e.fillMarket(ctx, strategyID, req, event)
}

// CheckPending checks all in-memory pending limit orders against the current price
// and fills any that have reached their limit price.
func (e *Executor) CheckPending(ctx context.Context, event PriceEvent) {
	for strategyID, orders := range e.pending {
		var remaining []*db.Order
		for _, o := range orders {
			if !e.shouldFill(o, event) {
				remaining = append(remaining, o)
				continue
			}
			execPrice := e.calcExecPrice(o.Side, event)
			fee := execPrice * o.Amount * e.feeRate
			if err := e.db.FillPendingOrder(ctx, o.ID, execPrice, fee, time.Now().UTC()); err != nil {
				slog.Error("fill pending order failed", "order_id", o.ID, "error", err)
				remaining = append(remaining, o)
				continue
			}
			e.broadcastFill(strategyID, o.Side, execPrice, o.Amount)
			slog.Info("limit order filled",
				"strategy_id", strategyID,
				"order_id", o.ID,
				"side", o.Side,
				"exec_price", execPrice)
		}
		e.pending[strategyID] = remaining
	}
}

func (e *Executor) fillMarket(ctx context.Context, strategyID int64, req *OrderRequest, event PriceEvent) {
	execPrice := e.calcExecPrice(req.Side, event)
	fee := execPrice * req.Amount * e.feeRate
	now := time.Now().UTC()

	o := &db.Order{
		StrategyID: strategyID,
		Side:       req.Side,
		Amount:     req.Amount,
		OrderPrice: event.Last,
		ExecPrice:  &execPrice,
		Fee:        &fee,
		FilledAt:   &now,
	}
	if err := e.db.FillOrder(ctx, o); err != nil {
		slog.Error("fill market order failed", "strategy_id", strategyID, "side", req.Side, "error", err)
		return
	}
	e.broadcastFill(strategyID, req.Side, execPrice, req.Amount)
	slog.Info("market order filled",
		"strategy_id", strategyID,
		"side", req.Side,
		"amount", req.Amount,
		"exec_price", execPrice,
		"fee", fee)
}

func (e *Executor) insertPending(ctx context.Context, strategyID int64, req *OrderRequest, event PriceEvent) {
	o := &db.Order{
		StrategyID: strategyID,
		Side:       req.Side,
		Amount:     req.Amount,
		LimitPrice: req.LimitPrice,
		OrderPrice: event.Last,
	}
	if err := e.db.InsertPendingOrder(ctx, o); err != nil {
		slog.Error("insert pending order failed", "strategy_id", strategyID, "error", err)
		return
	}
	e.pending[strategyID] = append(e.pending[strategyID], o)
	slog.Info("limit order pending",
		"strategy_id", strategyID,
		"side", req.Side,
		"limit_price", *req.LimitPrice)
}

func (e *Executor) shouldFill(o *db.Order, event PriceEvent) bool {
	if o.LimitPrice == nil {
		return false
	}
	switch o.Side {
	case "BUY":
		return event.Ask <= *o.LimitPrice
	case "SELL":
		return event.Bid >= *o.LimitPrice
	}
	return false
}

func (e *Executor) calcExecPrice(side string, event PriceEvent) float64 {
	switch side {
	case "BUY":
		return event.Ask * (1 + e.slippageRate)
	case "SELL":
		return event.Bid * (1 - e.slippageRate)
	}
	return event.Last
}
