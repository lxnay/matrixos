package main

import (
	"fmt"
	"matrixos/dev/janitor/cleaners"
	"matrixos/dev/janitor/config"
	"os"
)

func main() {
	// Initialize all the known cleaners.

	// For now, we just use a static config. In the future,
	// we will be using the real and full matrixOS config.
	cfg := config.StaticConfig{}
	if err := cfg.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Initializing images cleaner ...")
	icln := &cleaners.ImagesCleaner{}
	if err := icln.Init(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing images cleaner: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Initializing downloads cleaner ...")
	dcln := &cleaners.DownloadsCleaner{}
	if err := dcln.Init(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing downloads cleaner: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Initializing logs cleaner ...")
	lcln := &cleaners.LogsCleaner{}
	if err := lcln.Init(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing logs cleaner: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Initializing all cleaners ...")
	clnrs := []cleaners.ICleaner{
		icln,
		dcln,
		lcln,
	}

	exitSt := 0
	for _, cln := range clnrs {
		fmt.Printf("Starting cleaner: %s\n", cln.Name())
		if err := cln.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error executing cleaner %s: %v\n", cln.Name(), err)
			exitSt = 1
		}
	}
	os.Exit(exitSt)
}
