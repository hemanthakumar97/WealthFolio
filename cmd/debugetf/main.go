package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/hemanthhku/wealthfolio-v2/internal/services"
)

func main() {
	ctx := context.Background()
	pool, _ := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	defer pool.Close()

	m, err := services.FetchETFMetrics(ctx, 10, "NIFTYBEES.NS", pool, 20*time.Second)
	if err != nil {
		fmt.Println("ERROR:", err)
		return
	}
	fmt.Printf("Score: %d/100\n", m.Zero1Score)
	fmt.Printf("AvailableMax: %d\n", m.AvailableMax)
	fmt.Printf("DataGaps: %v\n", m.DataGaps)
	fmt.Printf("Beta: %.2f, AUM: %.0f Cr, TER: %.2f%%\n", m.Beta, m.AUMCr, m.TER)
	fmt.Printf("StdDev1Y: %.2f%%, StdDev5YMedian: %.2f%%\n", m.StdDev1Y, m.StdDev5YMedian)
	fmt.Printf("Rolling3YAvg: %.2f%%, Sharpe: %.3f\n", m.Rolling3YAvg, m.Sharpe1Y)
}
