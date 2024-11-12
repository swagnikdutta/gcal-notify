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
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/api/calendar/v3"
)

type Event struct {
	Summary     string
	Description string
	StartTime   string
	EndTime     string
}

type Notifier struct {
	Service                  *calendar.Service
	Events                   []*Event
	EventNotificationChannel *calendar.Channel
}

func NewNotifier() *Notifier {
	notifier := new(Notifier)
	notifier.Service = authenticate()
	notifier.Events = make([]*Event, 0)
	return notifier
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

	// Double check if this is the right way
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

	return nil
}

func getChannelId() string {
	splits := strings.Split(time.Now().String(), " ")
	splits2 := strings.Split(splits[1], ".")[0]
	s := strings.ReplaceAll(splits2, ":", "-")
	return s
}

func main() {
	notifier := NewNotifier()
	_ = notifier.updateEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		_ = notifier.updateEvents()
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
	})
	server := &http.Server{
		Addr:    "localhost:8080",
		Handler: mux,
	}

	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	wg.Add(2)

	// goroutine to run http server
	go func() {
		defer wg.Done()
		log.Println("Starting http server")
		err := server.ListenAndServe()
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
		defer wg.Done()
		<-stopCh
		_ = server.Shutdown(context.Background())
	}()

	ch, err := notifier.Service.Events.Watch("primary", &calendar.Channel{
		Id:         "test-channel-6",
		Address:    "https://8515-2405-201-8011-7043-8c50-c84-9901-5609.ngrok-free.app/webhook",
		Expiration: time.Now().Add(time.Minute).UnixMilli(),
		Type:       "web_hook",
	}).Do()
	if err != nil {
		log.Fatalf("Error watching events: %s\n", err.Error())
	}
	notifier.EventNotificationChannel = ch

	wg.Wait()
}
