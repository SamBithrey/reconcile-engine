package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	outDir := flag.String("out-dir", "seed-out", "Directory to write generated POs, GRNs, and mixed-format invoice fixtures")
	databaseURL := flag.String("database-url", "", "PostgreSQL URL to seed suppliers, 15 POs, and 14 GRNs (optional)")
	apiURL := flag.String("api-url", "", "Ingestion API base URL to submit the 20 mixed-format invoices (optional)")
	wait := flag.Bool("wait", false, "Wait for all submitted invoices to finish async parsing and 3-way matching")
	waitTimeout := flag.Duration("wait-timeout", 45*time.Second, "Maximum time to wait for async reconciliation when --wait is enabled")
	regenerate := flag.Bool("regenerate", false, "Force regenerating fixture files even if out-dir already contains a manifest")
	flag.Parse()

	var (
		summary *FixtureManifest
		err     error
	)
	if !*regenerate && (*databaseURL != "" || *apiURL != "") {
		summary, err = LoadExistingManifest(*outDir)
	}
	if summary == nil {
		summary, err = GenerateFixtures(*outDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "seed error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Generated fixtures in %s:\n", *outDir)
	} else {
		fmt.Printf("Reusing existing fixtures in %s:\n", *outDir)
	}
	fmt.Printf("  Suppliers:       %d\n", len(summary.Suppliers))
	fmt.Printf("  Purchase Orders: %d\n", len(summary.PurchaseOrders))
	fmt.Printf("  Goods Receipts:  %d\n", len(summary.GoodsReceipts))
	fmt.Printf("  Invoices:        %d\n", len(summary.Invoices))

	ctx, cancel := context.WithTimeout(context.Background(), *waitTimeout)
	defer cancel()

	if *databaseURL != "" {
		if err := SeedDatabase(ctx, *databaseURL, summary); err != nil {
			fmt.Fprintf(os.Stderr, "database seed error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Seeded PostgreSQL: %d suppliers, %d POs, %d GRNs\n",
			len(summary.Suppliers), len(summary.PurchaseOrders), len(summary.GoodsReceipts))
	}

	if *apiURL != "" {
		submitted, err := SubmitInvoicesToAPI(ctx, *apiURL, *outDir, summary)
		if err != nil {
			fmt.Fprintf(os.Stderr, "API ingestion error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Submitted %d invoices to %s\n", len(submitted), *apiURL)

		if *wait {
			details, err := WaitForReconciliation(ctx, *apiURL, submitted)
			if err != nil {
				fmt.Fprintf(os.Stderr, "reconciliation wait error: %v\n", err)
				os.Exit(1)
			}
			mismatches := 0
			for i, d := range details {
				sub := submitted[i]
				mark := "OK"
				if d.Invoice.Status != sub.Meta.ExpectedStatus {
					mark = fmt.Sprintf("MISMATCH (want %s)", sub.Meta.ExpectedStatus)
					mismatches++
				}
				fmt.Printf("  [%s] %-32s %-7s -> %-14s (discrepancies=%d) %s\n",
					mark, sub.Meta.Filename, sub.Meta.Format, d.Invoice.Status, len(d.Discrepancies), d.Invoice.ID)
			}
			if mismatches > 0 {
				fmt.Fprintf(os.Stderr, "reconciliation verification failed: %d unexpected invoice status(es)\n", mismatches)
				os.Exit(1)
			}
		}
	}
}
