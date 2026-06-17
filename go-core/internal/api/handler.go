package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"cc-poc/internal/db"
	"cc-poc/internal/hub"
)

//go:embed static/index.html
var indexHTML []byte

type Handler struct {
	db  *db.DB
	hub *hub.Hub
}

func New(database *db.DB, h *hub.Hub) *Handler {
	return &Handler{db: database, hub: h}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", h.serveIndex)
	mux.HandleFunc("/api/portfolio", h.portfolio)
	mux.HandleFunc("/api/orders", h.orders)
	mux.HandleFunc("/events", h.events)
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (h *Handler) portfolio(w http.ResponseWriter, r *http.Request) {
	summaries, err := h.db.GetPortfolioSummaries(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type row struct {
		Strategy    string  `json:"strategy"`
		InitialCash float64 `json:"initial_cash"`
		CashJPY     float64 `json:"cash_jpy"`
		BTCAmount   float64 `json:"btc_amount"`
	}
	resp := make([]row, len(summaries))
	for i, s := range summaries {
		resp[i] = row{s.StrategyName, s.InitialCash, s.CashJPY, s.BTCAmount}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) orders(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	orders, err := h.db.GetFilledOrders(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type row struct {
		ID        int64   `json:"id"`
		Strategy  string  `json:"strategy"`
		Side      string  `json:"side"`
		ExecPrice float64 `json:"exec_price"`
		Amount    float64 `json:"amount"`
		Fee       float64 `json:"fee"`
		FilledAt  string  `json:"filled_at"`
	}
	resp := make([]row, len(orders))
	for i, o := range orders {
		resp[i] = row{o.ID, o.StrategyName, o.Side, o.ExecPrice, o.Amount, o.Fee,
			o.FilledAt.Format(time.RFC3339)}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := h.hub.Subscribe()
	defer cancel()

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}
