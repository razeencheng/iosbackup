package main

import (
	"fmt"
	"iosbackup/internal/config"
	"os"
	"time"
)

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println(cfg.Timezone, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).In(cfg.Location).Format(time.RFC3339))
}
