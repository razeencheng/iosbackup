package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"iosbackup/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(exitErrorText(err))
	}
}

func exitErrorText(err error) string {
	return err.Error()
}
