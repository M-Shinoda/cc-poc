package strategy

import "cc-poc/internal/engine"

// MACross signals BUY on a bullish MA crossover and SELL on a bearish one.
// It holds at most one position at a time.
type MACross struct {
	id           int64
	name         string
	shortPeriod  int
	longPeriod   int
	tradeAmount  float64
	slippageRate float64
	feeRate      float64
	costBasis    float64 // buy exec price per BTC including fees

	prices      []float64
	prevShortMA float64
	prevLongMA  float64
	inPosition  bool
}

func NewMACross(id int64, name string, shortPeriod, longPeriod int, tradeAmount, slippageRate, feeRate float64) *MACross {
	return &MACross{
		id:           id,
		name:         name,
		shortPeriod:  shortPeriod,
		longPeriod:   longPeriod,
		tradeAmount:  tradeAmount,
		slippageRate: slippageRate,
		feeRate:      feeRate,
	}
}

// SetCostBasis restores the cost basis from a previously filled BUY order (used at startup).
func (s *MACross) SetCostBasis(price float64) { s.costBasis = price }

func (s *MACross) ID() int64    { return s.id }
func (s *MACross) Name() string { return s.name }

func (s *MACross) Warmup(prices []engine.PriceEvent) {
	for _, p := range prices {
		s.prices = append(s.prices, p.Last)
	}
	s.prices = trimSlice(s.prices, s.longPeriod*3)
	if len(s.prices) >= s.longPeriod {
		s.prevShortMA = sma(s.prices, s.shortPeriod)
		s.prevLongMA = sma(s.prices, s.longPeriod)
	}
}

// SetPosition initialises inPosition from portfolio state loaded at startup.
func (s *MACross) SetPosition(btcAmount float64) {
	s.inPosition = btcAmount > 0
}

func (s *MACross) OnPrice(event engine.PriceEvent) *engine.OrderRequest {
	s.prices = append(s.prices, event.Last)
	s.prices = trimSlice(s.prices, s.longPeriod*3)

	if len(s.prices) < s.longPeriod {
		return nil
	}

	shortMA := sma(s.prices, s.shortPeriod)
	longMA := sma(s.prices, s.longPeriod)

	var req *engine.OrderRequest

	// Bullish crossover
	if s.prevShortMA <= s.prevLongMA && shortMA > longMA && !s.inPosition {
		req = &engine.OrderRequest{Side: "BUY", Type: "market", Amount: s.tradeAmount}
		s.costBasis = event.Ask * (1 + s.slippageRate) * (1 + s.feeRate)
		s.inPosition = true
	}
	// Bearish crossover — only sell when net proceeds exceed cost basis
	if s.prevShortMA >= s.prevLongMA && shortMA < longMA && s.inPosition {
		estimatedNet := event.Bid * (1 - s.slippageRate) * (1 - s.feeRate)
		if estimatedNet > s.costBasis {
			req = &engine.OrderRequest{Side: "SELL", Type: "market", Amount: s.tradeAmount}
			s.inPosition = false
		}
	}

	s.prevShortMA = shortMA
	s.prevLongMA = longMA
	return req
}
