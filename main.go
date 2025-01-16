package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"google.golang.org/api/calendar/v3"
)

const (
	channelTypeWebhook          = "web_hook"
	notificationChannelEndpoint = "NOTIFICATION_CHANNEL_ENDPOINT"
	calendarId                  = "CALENDAR_ID"
	bulbStateOff                = "off"
	bulbStateOn                 = "on"
)

func NewRequestMultiplexer(h http.HandlerFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/events/notify", h)

	var handler http.Handler = mux
	// add middlewares if needed
	// handler = someMiddleware(handler)
	return handler
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	notifier := NewNotifier()
	err := notifier.updateEvents()
	if err != nil {
		log.Fatalf("Error updating events: %s", err)
	}

	// TODO: why signal channel needs to be buffered?
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)

	notifier.Wg.Add(3)

	httpServer := &http.Server{
		Addr:    "localhost:8080",
		Handler: NewRequestMultiplexer(notifier.ServeHTTP),
	}
	// goroutine to run http server
	go func(n *Notifier) {
		defer notifier.Wg.Done()
		log.Printf("HTTP server listening on %s\n", httpServer.Addr)

		err := httpServer.ListenAndServe()
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				log.Println("Shutting down server gracefully")

				err3 := n.Service.Channels.Stop(n.EventNotificationChannel).Do()
				if err3 != nil {
					log.Println("Error closing channel")
				} else {
					// We might not need to print this
					log.Println("Closed notification channel successfully.")
				}
				notifier.done <- struct{}{}
				return
			}
			log.Fatalf("ListenAndServe Error: %s\n", err)
		}
	}(notifier)

	// goroutine to handle manual server shutdown
	go func() {
		defer notifier.Wg.Done()
		<-stopCh
		_ = httpServer.Shutdown(context.Background())
	}()

	ch, err := notifier.Service.Events.Watch(os.Getenv(calendarId), &calendar.Channel{
		Id:      "test-channel-9",
		Address: fmt.Sprintf("%s/api/v1/events/notify", os.Getenv(notificationChannelEndpoint)),
		Type:    channelTypeWebhook,
	}).Do()
	if err != nil {
		log.Fatalf("Error watching events: %s\n", err.Error())
	}
	notifier.EventNotificationChannel = ch

	go notifier.watch()

	notifier.Wg.Wait()
}
