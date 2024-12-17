package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"google.golang.org/api/calendar/v3"
)

const (
	channelTypeWebhook          = "web_hook"
	notificationChannelEndpoint = "NOTIFICATION_CHANNEL_ENDPOINT"
	calendarId                  = "CALENDAR_ID"
)

func NewNotifier() *Notifier {
	return &Notifier{
		Service:                  authenticate(),
		Events:                   make([]*Event, 0),
		MergedEvents:             make([]*Event, 0),
		EventNotificationChannel: nil,
		Wg:                       &sync.WaitGroup{},
	}
}

func (n *Notifier) updateEvents() error {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).Format(time.RFC3339)
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local).Format(time.RFC3339)
	events, err := n.Service.Events.List("primary").TimeMin(start).TimeMax(end).Do()
	if err != nil {
		log.Printf("Error updating events. Error: %s\n", err.Error())
		return err
	}

	// Double check if this is the right way to empty a slice.
	if len(n.Events) > 0 {
		n.Events = nil
	}
	for _, ei := range events.Items {
		e := &Event{
			Summary:     ei.Summary,
			Description: ei.Description,
			StartTime:   ei.Start.DateTime,
			EndTime:     ei.End.DateTime,
		}
		n.Events = append(n.Events, e)
	}

	// TODO: sort the events by start time
	log.Println("Events in calendar:")
	for _, e := range n.Events {
		fmt.Printf("Event: %s\nStart: %s\nEnd: %s\n\n", e.Summary, e.StartTime, e.EndTime)
	}

	if len(n.MergedEvents) > 0 {
		n.MergedEvents = nil
	}

	for _, currEvent := range n.Events {
		if len(n.MergedEvents) == 0 {
			n.MergedEvents = append(n.MergedEvents, currEvent)
			continue
		}

		totalMergedEvents := len(n.MergedEvents)
		lastMergedEvent := n.MergedEvents[totalMergedEvents-1]

		if lastMergedEvent.overlapsWith(currEvent) {
			e := &Event{
				Summary:     fmt.Sprintf("%s:%s", lastMergedEvent.Summary, currEvent.Summary),
				Description: fmt.Sprintf("%s:%s", lastMergedEvent.Description, currEvent.Description),
				StartTime:   lastMergedEvent.StartTime,
				EndTime:     currEvent.EndTime,
			}
			n.MergedEvents[totalMergedEvents-1] = e
		} else {
			n.MergedEvents = append(n.MergedEvents, currEvent)
		}
	}

	log.Println("Merged events in calendar:")
	for _, e := range n.MergedEvents {
		fmt.Printf("Event: %s\nStart: %s\nEnd: %s\n\n", e.Summary, e.StartTime, e.EndTime)
	}

	return nil
}

func (n *Notifier) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = n.updateEvents()
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Println("Error reading request body")
		return
	}
	// r.Body.Close()
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	bodyString := string(bodyBytes)
	log.Printf("bodyString: %s", bodyString)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("200 OK"))
}

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
	_ = notifier.updateEvents()

	// TODO: why signal channel needs to be buffered?
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)

	notifier.Wg.Add(2)

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
				// TODO: handle channel closure
				err3 := n.Service.Channels.Stop(n.EventNotificationChannel).Do()
				if err3 != nil {
					log.Println("Error closing channel")
				}
				log.Println("Closed notification channel successfully.")
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
		Id:      "test-channel-8",
		Address: fmt.Sprintf("%s/api/v1/events/notify", os.Getenv(notificationChannelEndpoint)),
		// Expiration: time.Now().Add(time.Minute).UnixMilli(),
		Type: channelTypeWebhook,
	}).Do()
	if err != nil {
		log.Fatalf("Error watching events: %s\n", err.Error())
	}
	notifier.EventNotificationChannel = ch

	notifier.Wg.Wait()
}
