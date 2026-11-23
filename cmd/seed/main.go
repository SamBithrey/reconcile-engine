package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	outDir := flag.String("out-dir", "seed-out", "Directory to write generated POs, GRNs, and mixed-format invoice fixtures")
	flag.Parse()

	summary, err := GenerateFixtures(*outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated fixtures in %s:\n", *outDir)
	fmt.Printf("  Suppliers:       %d\n", len(summary.Suppliers))
	fmt.Printf("  Purchase Orders: %d\n", len(summary.PurchaseOrders))
	fmt.Printf("  Goods Receipts:  %d\n", len(summary.GoodsReceipts))
	fmt.Printf("  Invoices:        %d\n", len(summary.Invoices))
}
