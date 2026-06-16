package engine

import "time"

type PriceEvent struct {
	Pair       string
	Last       float64
	Bid        float64
	Ask        float64
	Volume     float64
	RecordedAt time.Time
}

type OrderRequest struct {
	Side       string   // "BUY" or "SELL"
	Type       string   // "market" or "limit"
	Amount     float64  // BTC
	LimitPrice *float64 // nil for market orders
}
