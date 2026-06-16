package strategy

import "cc-poc/internal/engine"

// Strategy is the interface all trading strategies must implement.
type Strategy interface {
	ID() int64
	Name() string
	// Warmup feeds historical prices to the strategy so indicators are
	// meaningful before live data arrives.
	Warmup(prices []engine.PriceEvent)
	// OnPrice processes one price tick and returns an order request or nil.
	OnPrice(event engine.PriceEvent) *engine.OrderRequest
}

// sma computes a simple moving average of the last `period` elements.
func sma(prices []float64, period int) float64 {
	if len(prices) < period {
		return 0
	}
	sum := 0.0
	for _, p := range prices[len(prices)-period:] {
		sum += p
	}
	return sum / float64(period)
}

// rsi computes the RSI over the last `period+1` prices using simple averages.
func rsi(prices []float64, period int) float64 {
	need := period + 1
	if len(prices) < need {
		return 50 // neutral when data is insufficient
	}
	slice := prices[len(prices)-need:]
	gains, losses := 0.0, 0.0
	for i := 1; i < len(slice); i++ {
		diff := slice[i] - slice[i-1]
		if diff > 0 {
			gains += diff
		} else {
			losses -= diff
		}
	}
	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)
	if avgLoss == 0 {
		return 100
	}
	return 100 - (100 / (1 + avgGain/avgLoss))
}

// trimSlice keeps only the last maxLen elements to bound memory usage.
func trimSlice(prices []float64, maxLen int) []float64 {
	if len(prices) > maxLen {
		return prices[len(prices)-maxLen:]
	}
	return prices
}
