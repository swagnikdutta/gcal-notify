package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"sync"
	"time"

	"google.golang.org/api/calendar/v3"
)

type Event struct {
	Summary     string
	Description string
	StartTime   string
	EndTime     string
}

func (e1 *Event) overlapsWith(e2 *Event) bool {
	e1EndTime, err := time.Parse(time.RFC3339, e1.EndTime)
	if err != nil {
		log.Printf("Error parsing time: %s\n", err)
	}
	e2StartTime, _ := time.Parse(time.RFC3339, e2.StartTime)
	if err != nil {
		log.Printf("Error parsing time: %s\n", err)
	}
	if e1EndTime.Compare(e2StartTime) == -1 {
		return false
	}
	return true
}

type Notifier struct {
	bulbState                string
	Service                  *calendar.Service
	Events                   []*Event
	UpcomingEvent            *Event
	MergedEvents             []*Event
	EventNotificationChannel *calendar.Channel
	Wg                       *sync.WaitGroup
	t                        *time.Ticker
	done                     chan struct{}
}

func NewNotifier() *Notifier {
	return &Notifier{
		bulbState:                bulbStateOff,
		Service:                  authenticate(),
		Events:                   make([]*Event, 0),
		MergedEvents:             make([]*Event, 0),
		UpcomingEvent:            nil,
		EventNotificationChannel: nil,
		Wg:                       &sync.WaitGroup{},
		t:                        time.NewTicker(1 * time.Minute),
		done:                     make(chan struct{}),
	}
}

func (n *Notifier) updateEvents() error {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).Format(time.RFC3339)
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local).Format(time.RFC3339)
	events, err := n.Service.Events.List("primary").TimeMin(startOfDay).TimeMax(endOfDay).Do()
	if err != nil {
		log.Printf("Error updating events. Error: %s\n", err.Error())
		return err
	}
	// TODO: Double check if this is the right way to empty a slice.
	if len(n.Events) > 0 {
		n.Events = nil
	}
	for _, ei := range events.Items {
		// TODO: read the documentation for the statuses
		if ei.Status == "cancelled" {
			continue
		}
		e := &Event{
			Summary:     ei.Summary,
			Description: ei.Description,
			StartTime:   ei.Start.DateTime,
			EndTime:     ei.End.DateTime,
		}
		n.Events = append(n.Events, e)
	}
	slices.SortFunc(n.Events, func(e1, e2 *Event) int {
		a, _ := time.Parse(time.RFC3339, e1.StartTime)
		b, _ := time.Parse(time.RFC3339, e2.StartTime)
		return a.Compare(b)
	})

	// log.Println("Events in calendar:")
	// for _, e := range n.Events {
	// 	log.Printf("Event: %s\tStart: %s\tEnd: %s", e.Summary, e.StartTime, e.EndTime)
	// }
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
	// log.Println("Merged events in calendar:")
	// for _, e := range n.MergedEvents {
	// 	log.Printf("Event: %s\tStart: %s\tEnd: %s", e.Summary, e.StartTime, e.EndTime)
	// }
	n.setUpcomingEvent()
	return nil
}

func (n *Notifier) setUpcomingEvent() {
	n.UpcomingEvent = nil // set this to null once the most recent event ends
	for _, mergedEvent := range n.MergedEvents {
		if n.UpcomingEvent == nil {
			n.UpcomingEvent = mergedEvent
			continue
		}
		currentTime := time.Now()
		mergedEventStartTime, _ := time.Parse(time.RFC3339, mergedEvent.StartTime)
		upcomingEventStartTime, _ := time.Parse(time.RFC3339, n.UpcomingEvent.StartTime)
		if currentTime.Before(mergedEventStartTime) && mergedEventStartTime.Before(upcomingEventStartTime) {
			n.UpcomingEvent = mergedEvent
		}
	}
	log.Printf("Upcoming event is: %q, starts at: %q", n.UpcomingEvent.Summary, n.UpcomingEvent.StartTime)
}

func (n *Notifier) upcomingEventStarted() bool {
	now := time.Now()
	st, _ := time.Parse(time.RFC3339, n.UpcomingEvent.StartTime)
	et, _ := time.Parse(time.RFC3339, n.UpcomingEvent.EndTime)
	if now.After(st) && now.Before(et) {
		return true
	}
	return false
}

func (n *Notifier) upcomingEventEnded() bool {
	now := time.Now()
	et, _ := time.Parse(time.RFC3339, n.UpcomingEvent.EndTime)
	if now.After(et) {
		return true
	}
	return false
}

func (n *Notifier) watch() {
	defer n.Wg.Done()
	for {
		select {
		case <-n.done:
			fmt.Println("Stopping the ticker")
			return
		case <-n.t.C:
			if n.UpcomingEvent == nil {
				continue
			}
			fmt.Println("upcoming event: ", n.UpcomingEvent.Summary)
			if n.upcomingEventStarted() {
				fmt.Println("upcoming event has started")
				if n.bulbState == bulbStateOff {
					n.bulbState = bulbStateOn
				}
				// notify all
			}
			if n.upcomingEventEnded() {
				fmt.Println("upcoming event has ended")
				n.bulbState = bulbStateOff
				// update upcoming event
				n.setUpcomingEvent()
			}
		}
	}
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
