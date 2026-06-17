"""
CoinCheck Paper Trader — 損益レポートスクリプト

使い方:
  docker compose run python-analyzer python report.py
  docker compose run python-analyzer python report.py --chart
"""

import os
import sys
import argparse
import psycopg2
import pandas as pd
from tabulate import tabulate

DATABASE_URL = os.environ["DATABASE_URL"]


def get_connection():
    return psycopg2.connect(DATABASE_URL)


def load_strategies(conn) -> pd.DataFrame:
    return pd.read_sql(
        "SELECT id, name, initial_cash FROM strategies ORDER BY id",
        conn,
    )


def load_portfolios(conn) -> pd.DataFrame:
    return pd.read_sql(
        """SELECT p.strategy_id, p.cash_jpy, p.btc_amount, p.updated_at, s.name
           FROM portfolios p JOIN strategies s ON s.id = p.strategy_id
           ORDER BY p.strategy_id""",
        conn,
    )


def load_orders(conn) -> pd.DataFrame:
    return pd.read_sql(
        """SELECT o.id, o.strategy_id, s.name AS strategy_name,
                  o.side, o.status, o.amount,
                  o.order_price, o.exec_price, o.fee,
                  o.created_at, o.filled_at
           FROM orders o JOIN strategies s ON s.id = o.strategy_id
           WHERE o.status = 'filled'
           ORDER BY o.filled_at""",
        conn,
    )


def load_recent_prices(conn, hours: int = 24) -> pd.DataFrame:
    return pd.read_sql(
        f"""SELECT pair, last, bid, ask, recorded_at
            FROM price_history
            WHERE recorded_at >= NOW() - INTERVAL '{hours} hours'
            ORDER BY recorded_at""",
        conn,
    )


def print_summary(portfolios: pd.DataFrame, strategies: pd.DataFrame, current_price: float):
    rows = []
    for _, p in portfolios.iterrows():
        sc = strategies[strategies["id"] == p["strategy_id"]]
        initial = float(sc["initial_cash"].iloc[0]) if len(sc) > 0 else 1_000_000
        btc_val = p["btc_amount"] * current_price
        total = p["cash_jpy"] + btc_val
        pnl = total - initial
        pnl_pct = pnl / initial * 100
        rows.append({
            "Strategy": p["name"],
            "Cash (JPY)": f"¥{p['cash_jpy']:,.0f}",
            "BTC": f"{p['btc_amount']:.8f}",
            "BTC Value": f"¥{btc_val:,.0f}",
            "Total (JPY)": f"¥{total:,.0f}",
            "P&L": f"¥{pnl:+,.0f}",
            "P&L %": f"{pnl_pct:+.2f}%",
        })

    print(f"\n{'═'*60}")
    print(f"  CoinCheck Paper Trader  BTC/JPY: ¥{current_price:,.0f}")
    print(f"{'═'*60}")
    print(tabulate(rows, headers="keys", tablefmt="rounded_outline"))


def print_trade_history(orders: pd.DataFrame):
    if orders.empty:
        print("\n取引履歴なし")
        return

    rows = []
    for _, o in orders.iterrows():
        filled_at = o["filled_at"]
        if hasattr(filled_at, "strftime"):
            filled_at = filled_at.strftime("%Y-%m-%d %H:%M:%S")
        rows.append({
            "ID": o["id"],
            "Strategy": o["strategy_name"],
            "Side": o["side"],
            "Amount (BTC)": f"{o['amount']:.8f}",
            "Exec Price": f"¥{o['exec_price']:,.0f}",
            "Fee (JPY)": f"¥{o['fee']:,.2f}",
            "Filled At": filled_at,
        })

    print(f"\n{'─'*60}")
    print("  売買履歴一覧")
    print(tabulate(rows, headers="keys", tablefmt="rounded_outline"))


def print_trade_stats(orders: pd.DataFrame):
    if orders.empty:
        return

    stats = []
    for name, grp in orders.groupby("strategy_name"):
        sells = grp[grp["side"] == "SELL"]
        buys = grp[grp["side"] == "BUY"]
        total_fee = grp["fee"].sum()
        stats.append({
            "Strategy": name,
            "BUY": len(buys),
            "SELL": len(sells),
            "Total Fee (JPY)": f"¥{total_fee:,.0f}",
        })

    print(f"\n{'─'*60}")
    print("  取引統計")
    print(tabulate(stats, headers="keys", tablefmt="rounded_outline"))


def draw_chart(prices: pd.DataFrame, orders: pd.DataFrame):
    try:
        import matplotlib.pyplot as plt
        import matplotlib.dates as mdates
    except ImportError:
        print("matplotlib not available")
        return

    if prices.empty:
        print("価格データなし")
        return

    fig, ax = plt.subplots(figsize=(14, 6))
    ax.plot(prices["recorded_at"], prices["last"], linewidth=1, label="BTC/JPY")

    colors = {"ma_cross_5_20": "tab:blue", "rsi_14": "tab:orange"}
    markers = {"BUY": ("^", "green"), "SELL": ("v", "red")}

    for name, grp in orders.groupby("strategy_name"):
        color = colors.get(name, "gray")
        for side, (marker, mcolor) in markers.items():
            sub = grp[grp["side"] == side]
            if not sub.empty:
                ax.scatter(sub["filled_at"], sub["exec_price"],
                           marker=marker, color=mcolor, s=60,
                           label=f"{name} {side}", zorder=5)

    ax.xaxis.set_major_formatter(mdates.DateFormatter("%m/%d %H:%M"))
    ax.xaxis.set_major_locator(mdates.AutoDateLocator())
    fig.autofmt_xdate()
    ax.set_title("BTC/JPY 価格チャートと約定ポイント")
    ax.set_ylabel("Price (JPY)")
    ax.legend(loc="upper left", fontsize=8)
    ax.grid(True, alpha=0.3)
    plt.tight_layout()

    out = "/output/chart.png"
    plt.savefig(out, dpi=150)
    print(f"チャートを保存しました: {out}")


def main():
    parser = argparse.ArgumentParser(description="Paper trader report")
    parser.add_argument("--chart", action="store_true", help="チャートを出力する")
    parser.add_argument("--hours", type=int, default=24, help="価格履歴の取得時間（時間）")
    args = parser.parse_args()

    with get_connection() as conn:
        strategies = load_strategies(conn)
        portfolios = load_portfolios(conn)
        orders = load_orders(conn)
        prices = load_recent_prices(conn, hours=args.hours)

    current_price = float(prices["last"].iloc[-1]) if not prices.empty else 0.0

    print_summary(portfolios, strategies, current_price)
    print_trade_history(orders)
    print_trade_stats(orders)

    if args.chart:
        draw_chart(prices, orders)


if __name__ == "__main__":
    main()
