package main

import (
	"log"

	"github.com/antero-software/lazy-ssm/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
