package reporter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"cc-poc/internal/db"
)

type Console struct {
	db       *db.DB
	interval time.Duration
	lastRun  time.Time
}

func NewConsole(database *db.DB, interval time.Duration) *Console {
	return &Console{db: database, interval: interval}
}

// MaybeReport prints a summary if the reporting interval has elapsed.
func (r *Console) MaybeReport(ctx context.Context, currentPrice float64) {
	if time.Since(r.lastRun) < r.interval {
		return
	}
	r.lastRun = time.Now()

	portfolios, names, err := r.db.GetAllPortfolios(ctx)
	if err != nil {
		slog.Error("reporter: get portfolios", "error", err)
		return
	}

	fmt.Println("═══════════════════════════════════════════════════════")
	fmt.Printf("  CoinCheck Paper Trader   BTC/JPY: ¥%s\n",
		formatJPY(currentPrice))
	fmt.Printf("  %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println("───────────────────────────────────────────────────────")

	for i, p := range portfolios {
		btcVal := p.BTCAmount * currentPrice
		total := p.CashJPY + btcVal

		// initial_cash is not stored in Portfolio; read from strategies table would be needed.
		// We approximate P&L as total - 1,000,000 (default initial cash).
		// For accuracy, consider joining with strategies.initial_cash.
		pnl := total - 1_000_000
		pnlPct := pnl / 1_000_000 * 100

		orders, err := r.db.CountFilledOrders(ctx, p.StrategyID)
		if err != nil {
			orders = -1
		}

		sign := "+"
		if pnl < 0 {
			sign = ""
		}
		fmt.Printf("\n  ▸ %s\n", names[i])
		fmt.Printf("    Cash:    ¥%s\n", formatJPY(p.CashJPY))
		fmt.Printf("    BTC:     %.8f BTC  (¥%s)\n", p.BTCAmount, formatJPY(btcVal))
		fmt.Printf("    Total:   ¥%s\n", formatJPY(total))
		fmt.Printf("    P&L:     %s¥%s  (%s%.2f%%)\n",
			sign, formatJPY(pnl), sign, pnlPct)
		fmt.Printf("    Orders:  %d filled\n", orders)
	}
	fmt.Println("═══════════════════════════════════════════════════════")
}

func formatJPY(v float64) string {
	if v < 0 {
		return fmt.Sprintf("-%.0f", -v)
	}
	// simple comma formatting
	s := fmt.Sprintf("%.0f", v)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}
