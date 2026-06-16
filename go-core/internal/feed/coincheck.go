package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type Ticker struct {
	Last       float64
	Bid        float64
	Ask        float64
	Volume     float64
	RecordedAt time.Time
}

type tickerResponse struct {
	Last      float64 `json:"last"`
	Bid       float64 `json:"bid"`
	Ask       float64 `json:"ask"`
	Volume    float64 `json:"volume"`
	Timestamp int64   `json:"timestamp"`
}

type PriceFeed struct {
	client   *http.Client
	interval time.Duration
	out      chan<- Ticker
}

func NewPriceFeed(interval time.Duration, out chan<- Ticker) *PriceFeed {
	return &PriceFeed{
		client:   &http.Client{Timeout: 10 * time.Second},
		interval: interval,
		out:      out,
	}
}

// Run polls the CoinCheck ticker API until ctx is cancelled.
// API errors are retried with exponential backoff (max 60s).
func (f *PriceFeed) Run(ctx context.Context) {
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	f.fetchAndSend(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.fetchAndSend(ctx)
		}
	}
}

func (f *PriceFeed) fetchAndSend(ctx context.Context) {
	t, err := f.fetchWithRetry(ctx)
	if err != nil {
		return // context cancelled, already logged
	}
	select {
	case f.out <- *t:
	case <-ctx.Done():
	}
}

func (f *PriceFeed) fetchWithRetry(ctx context.Context) (*Ticker, error) {
	backoff := time.Second
	const maxBackoff = 60 * time.Second
	longDownAt := time.Time{}

	for {
		t, err := f.fetchOnce(ctx)
		if err == nil {
			if !longDownAt.IsZero() {
				slog.Info("coincheck api recovered", "down_duration", time.Since(longDownAt).Round(time.Second))
			}
			return t, nil
		}

		slog.Warn("coincheck api error", "error", err, "retry_in", backoff)
		if backoff >= 5*time.Minute && longDownAt.IsZero() {
			longDownAt = time.Now()
			slog.Error("coincheck api down for 5+ minutes, alert")
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (f *PriceFeed) fetchOnce(ctx context.Context) (*Ticker, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://coincheck.com/api/ticker", nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var tr tickerResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	recordedAt := time.Now().UTC()
	if tr.Timestamp > 0 {
		recordedAt = time.Unix(tr.Timestamp, 0).UTC()
	}
	return &Ticker{
		Last:       tr.Last,
		Bid:        tr.Bid,
		Ask:        tr.Ask,
		Volume:     tr.Volume,
		RecordedAt: recordedAt,
	}, nil
}
