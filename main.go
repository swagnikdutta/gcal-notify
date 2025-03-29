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

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"google.golang.org/api/calendar/v3"
)

const (
	channelTypeWebhook          = "web_hook"
	notificationChannelEndpoint = "NOTIFICATION_CHANNEL_ENDPOINT"
	calendarId                  = "CALENDAR_ID"
)

func NewRequestMultiplexer(h http.HandlerFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/events/notify", h)

	var handler http.Handler = mux
	// add middlewares if needed
	// handler = someMiddleware(handler)
	return handler
}

// This function essentially returns a channel - through which we will receive certain
// signals, in the future. We just need to keep listening to the channel for those signals.
func setupSignalHandler() chan os.Signal {
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)
	return stopCh
}

func createHTTPServer(notifier *Notifier) *http.Server {
	return &http.Server{
		// TODO: replace with actual endpoint
		Addr:    "localhost:8080",
		Handler: NewRequestMultiplexer(notifier.ServeHTTP),
	}
}

func startHTTPServer(server *http.Server, notifier *Notifier) {
	notifier.Wg.Add(1)
	go func() {
		defer notifier.Wg.Done()
		log.Printf("HTTP server listening on %s\n", server.Addr)

		err := server.ListenAndServe()
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				chanStopErr := notifier.Service.Channels.Stop(notifier.EventNotificationChannel).Do()
				if chanStopErr != nil {
					log.Printf("error stopping notification channel: %v", chanStopErr)
				}
				log.Println("notification channel stopped successfully")
				log.Println("shutting down server gracefully")
				notifier.done <- struct{}{}
				return
			}
			log.Fatalf("ListenAndServe Error: %s\n", err)
		}
	}()
}

func waitForShutdown(server *http.Server, notifier *Notifier, stopCh chan os.Signal) {
	notifier.Wg.Add(1)
	go func() {
		defer notifier.Wg.Done()
		<-stopCh
		_ = server.Shutdown(context.Background())
	}()
}

func startWatchingEvents(notifier *Notifier) {
	notifier.Wg.Add(1)
	go func() {
		defer notifier.Wg.Done()
		ch, err := notifier.Service.Events.Watch(os.Getenv(calendarId), &calendar.Channel{
			Id:      uuid.New().String(),
			Address: fmt.Sprintf("%s/api/v1/events/notify", os.Getenv(notificationChannelEndpoint)),
			Type:    channelTypeWebhook,
		}).Do()
		if err != nil {
			log.Fatalf("Error watching events: %s\n", err.Error())
		}

		notifier.EventNotificationChannel = ch
		notifier.watch()
	}()
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	notifier := NewNotifier()
	err := notifier.syncCalendar()
	if err != nil {
		log.Fatalf("Error syncing calendar: %s", err)
	}

	stopCh := setupSignalHandler()
	httpServer := createHTTPServer(notifier)
	startHTTPServer(httpServer, notifier)

	startWatchingEvents(notifier)
	waitForShutdown(httpServer, notifier, stopCh)

	// subscriber part begins
	hue := NewPhilipsHue()
	notifier.registerObserver(hue)

	notifier.Wg.Wait() // blocks
}
