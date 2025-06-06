package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"google.golang.org/api/calendar/v3"
)

func NewRequestMultiplexer(notifier *Notifier) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/notify", notifier.handleCalendarUpdates)
	mux.HandleFunc("/healthcheck", notifier.healthCheck)

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
		Addr:    ":8080",
		Handler: NewRequestMultiplexer(notifier),
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
			Address: fmt.Sprintf("%s/notify", os.Getenv(notificationChannelEndpoint)),
			Type:    channelTypeWebhook,
		}).Do()
		if err != nil {
			log.Fatalf("Error watching events: %s\n", err.Error())
		}

		notifier.EventNotificationChannel = ch
		notifier.watch()
	}()
}

func assertUserCalendarExistsInSvcAccount(notifier *Notifier) {
	calendarList, err := notifier.Service.CalendarList.List().Do()
	if err != nil {
		log.Fatalf("Error fetching calendar list: %s\n", err)
	}

	// ensure calendar with our calendar id exists
	idx := slices.IndexFunc(calendarList.Items, func(entry *calendar.CalendarListEntry) bool {
		return entry.Id == notifier.calendarId
	})
	if idx < 0 {
		entry := &calendar.CalendarListEntry{
			Id: notifier.calendarId,
		}
		entry, err := notifier.Service.CalendarList.Insert(entry).Do()
		if err != nil {
			log.Fatalf("Error inserting user's calendar to service account's calendar: %s\n", err)
		}
	}
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	notifier := NewNotifier()
	// ensure user's calendar exists in service account's calendar.
	assertUserCalendarExistsInSvcAccount(notifier)

	err := notifier.syncCalendar()
	if err != nil {
		log.Fatalf("Error syncing calendar: %s", err)
	}

	stopCh := setupSignalHandler()
	httpServer := createHTTPServer(notifier)
	startHTTPServer(httpServer, notifier)

	startWatchingEvents(notifier)
	waitForShutdown(httpServer, notifier, stopCh)

	notifier.Wg.Wait() // blocks
}
