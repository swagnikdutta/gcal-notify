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
	Service *calendar.Service
	Events  []*Event
}

func (n *Notifier) fetchEvents() {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).Format(time.RFC3339)
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local).Format(time.RFC3339)

	events, err := n.Service.Events.List("primary").TimeMin(start).TimeMax(end).Do()
	if err != nil {
		log.Printf("Error fetching ")
		return
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
}

func getChannelId() string {
	splits := strings.Split(time.Now().String(), " ")
	splits2 := strings.Split(splits[1], ".")[0]
	s := strings.ReplaceAll(splits2, ":", "-")
	return s
}

func main() {
	notifier := new(Notifier)
	notifier.Service = doAuth()
	notifier.Events = make([]*Event, 0)
	notifier.fetchEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Calendar updated!")
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			log.Println("Error reading request body")
			return
		}

		// r.Body.Close()
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		bodyString := string(bodyBytes)
		log.Printf("Response:\n%s\n", bodyString)

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
				fmt.Println("Shutting down server gracefully")
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

	channel := &calendar.Channel{
		Id:      getChannelId(),
		Address: "https://ffe5-2405-201-8011-7043-ac8f-e0f4-a4f1-9fbe.ngrok-free.app/webhook",
		Type:    "web_hook",
	}

	_, err := notifier.Service.Events.Watch("primary", channel).Do()
	if err != nil {
		log.Fatal("Error watching events: %s\n", err.Error())
	}

	wg.Wait()
}
