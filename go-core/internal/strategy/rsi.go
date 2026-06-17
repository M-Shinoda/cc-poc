package strategy

import "cc-poc/internal/engine"

// RSIStrategy signals BUY when RSI drops below oversold and SELL when it
// rises above overbought. It holds at most one position at a time.
type RSIStrategy struct {
	id           int64
	name         string
	period       int
	oversold     float64
	overbought   float64
	tradeAmount  float64
	slippageRate float64
	feeRate      float64
	costBasis    float64 // buy exec price per BTC including fees

	prices     []float64
	inPosition bool
}

func NewRSI(id int64, name string, period int, oversold, overbought, tradeAmount, slippageRate, feeRate float64) *RSIStrategy {
	return &RSIStrategy{
		id:           id,
		name:         name,
		period:       period,
		oversold:     oversold,
		overbought:   overbought,
		tradeAmount:  tradeAmount,
		slippageRate: slippageRate,
		feeRate:      feeRate,
	}
}

// SetCostBasis restores the cost basis from a previously filled BUY order (used at startup).
func (s *RSIStrategy) SetCostBasis(price float64) { s.costBasis = price }

func (s *RSIStrategy) ID() int64    { return s.id }
func (s *RSIStrategy) Name() string { return s.name }

func (s *RSIStrategy) Warmup(prices []engine.PriceEvent) {
	for _, p := range prices {
		s.prices = append(s.prices, p.Last)
	}
	s.prices = trimSlice(s.prices, (s.period+1)*3)
}

// SetPosition initialises inPosition from portfolio state loaded at startup.
func (s *RSIStrategy) SetPosition(btcAmount float64) {
	s.inPosition = btcAmount > 0
}

func (s *RSIStrategy) OnPrice(event engine.PriceEvent) *engine.OrderRequest {
	s.prices = append(s.prices, event.Last)
	s.prices = trimSlice(s.prices, (s.period+1)*3)

	if len(s.prices) < s.period+1 {
		return nil
	}

	r := rsi(s.prices, s.period)

	if r < s.oversold && !s.inPosition {
		s.costBasis = event.Ask * (1 + s.slippageRate) * (1 + s.feeRate)
		s.inPosition = true
		return &engine.OrderRequest{Side: "BUY", Type: "market", Amount: s.tradeAmount}
	}
	if r > s.overbought && s.inPosition {
		estimatedNet := event.Bid * (1 - s.slippageRate) * (1 - s.feeRate)
		if estimatedNet > s.costBasis {
			s.inPosition = false
			return &engine.OrderRequest{Side: "SELL", Type: "market", Amount: s.tradeAmount}
		}
	}
	return nil
}
