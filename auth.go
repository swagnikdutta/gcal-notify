package main

import (
	"context"
	"log"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

func authenticate() *calendar.Service {
	ctx := context.Background()
	svc, err := calendar.NewService(ctx, option.WithCredentialsFile("credentials.json"))
	if err != nil {
		log.Fatalf("Error: %s", err)
	}
	return svc
}
