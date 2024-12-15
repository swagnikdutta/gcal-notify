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

	"google.golang.org/api/calendar/v3"
)

const channelTypeWebhook = "web_hook"

func NewNotifier() *Notifier {
	return &Notifier{
		Service:                  authenticate(),
		Events:                   make([]*Event, 0),
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
			EndTime:     ei.Start.DateTime,
		}
		n.Events = append(n.Events, e)
	}

	log.Println("Events in calendar:")
	for _, e := range n.Events {
		fmt.Println(e.Summary)
	}

	// merge intervals

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
	_ = bodyString
	// log.Printf("Response:\n%s\n", bodyString)
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
	go func() {
		defer notifier.Wg.Done()
		log.Printf("HTTP server listening on %s\n", httpServer.Addr)

		err := httpServer.ListenAndServe()
		if err != nil {
			if errors.Is(err, http.ErrServerClosed) {
				log.Println("Shutting down server gracefully")
				return
			}
			log.Fatalf("ListenAndServe Error: %s\n", err)
		}
	}()

	// goroutine to handle manual server shutdown
	go func() {
		defer notifier.Wg.Done()
		<-stopCh
		_ = httpServer.Shutdown(context.Background())
	}()

	ch, err := notifier.Service.Events.Watch("primary", &calendar.Channel{
		Id:         "test-channel-6",
		Address:    "https://cab2-106-51-160-154.ngrok-free.app/api/v1/events/notify",
		Expiration: time.Now().Add(time.Minute).UnixMilli(),
		Type:       channelTypeWebhook,
	}).Do()
	if err != nil {
		log.Fatalf("Error watching events: %s\n", err.Error())
	}
	notifier.EventNotificationChannel = ch

	notifier.Wg.Wait()
}
