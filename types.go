package main

import (
	"fmt"
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
	IsRecurring bool
}

func (e *Event) updateStartTimeForRecurringEvent() {
	now := time.Now()

	startTime, _ := time.Parse(time.RFC3339, e.StartTime)
	updatedStartTime := time.Date(
		now.Year(), now.Month(), now.Day(),
		startTime.Hour(), startTime.Minute(), startTime.Second(), startTime.Nanosecond(), startTime.Location(),
	)
	e.StartTime = updatedStartTime.Format(time.RFC3339)
	// I am not doing the same thing for end times because
	// 1) They don't matter in sorting or in any of my logic
	// 2) I need to decide what to do if end time spans over midnight and goes on to the next day.
}

func (e1 *Event) partiallyOverlapsWith(e2 *Event) bool {
	e1EndTime, _ := parseTime(e1.EndTime)
	e2StartTime, _ := parseTime(e2.StartTime)
	e2EndTime, _ := parseTime(e2.EndTime)

	return (e1EndTime.Equal(e2StartTime) || e1EndTime.After(e2StartTime)) && e1EndTime.Before(e2EndTime)
}

func (e1 *Event) completelyOverlapsWith(e2 *Event) bool {
	e1EndTime, _ := parseTime(e1.EndTime)
	e2EndTime, _ := parseTime(e2.EndTime)

	return e1EndTime.Equal(e2EndTime) || e1EndTime.After(e2EndTime)
}

func (e *Event) inProgress() bool {
	now := time.Now()
	startTime, _ := time.Parse(time.RFC3339, e.StartTime)
	endTime, _ := time.Parse(time.RFC3339, e.EndTime)

	if (now.Equal(startTime) || now.After(startTime)) && (now.Equal(endTime) || now.Before(endTime)) {
		return true
	}
	return false
}

func (e *Event) hasEnded() bool {
	now := time.Now()
	endTime, _ := time.Parse(time.RFC3339, e.EndTime)

	if now.After(endTime) {
		return true
	}
	return false
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
	currentDay               int
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
		t:                        time.NewTicker(10 * time.Second),
		done:                     make(chan struct{}),
		currentDay:               time.Now().Day(),
	}
}

func (n *Notifier) populateEvents(events *calendar.Events) {
	n.Events = nil
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
			IsRecurring: ei.Recurrence != nil,
		}
		n.Events = append(n.Events, e)
	}

	fmt.Printf("\nlist of events in calendar(sorted by start time) - %d\n", len(n.Events))
	for idx, e := range n.Events {
		fmt.Printf("%d) %s\n", idx+1, e.Summary)
	}
	for _, e := range n.Events {
		if e.IsRecurring {
			e.updateStartTimeForRecurringEvent()
		}
	}
}

func (n *Notifier) mergeOverlappingEvents() {
	slices.SortFunc(n.Events, func(e1, e2 *Event) int {
		a, _ := time.Parse(time.RFC3339, e1.StartTime)
		b, _ := time.Parse(time.RFC3339, e2.StartTime)
		return a.Compare(b)
	})

	n.MergedEvents = nil
	for _, currEvent := range n.Events {
		if len(n.MergedEvents) == 0 {
			n.MergedEvents = append(n.MergedEvents, currEvent)
			continue
		}

		lastMergedEvent := n.MergedEvents[len(n.MergedEvents)-1]
		if lastMergedEvent.partiallyOverlapsWith(currEvent) {
			e := &Event{
				Summary:     fmt.Sprintf("%s:%s", lastMergedEvent.Summary, currEvent.Summary),
				Description: fmt.Sprintf("%s:%s", lastMergedEvent.Description, currEvent.Description),
				StartTime:   lastMergedEvent.StartTime,
				EndTime:     currEvent.EndTime,
			}
			n.MergedEvents[len(n.MergedEvents)-1] = e
		} else if lastMergedEvent.completelyOverlapsWith(currEvent) {
			// nothing to do here, because we don't push a completely engulfed event into merged events list
			continue
		} else {
			n.MergedEvents = append(n.MergedEvents, currEvent)
		}
	}

	fmt.Printf("\nlist of merged events in calendar(sorted by start time) - %d\n", len(n.MergedEvents))
	for idx, e := range n.MergedEvents {
		fmt.Printf("%d) %s\n", idx+1, e.Summary)
	}
	fmt.Println()
}

func (n *Notifier) setUpcomingEvent() {
	n.UpcomingEvent = nil // set this to null once the most recent event ends

	for _, mergedEvent := range n.MergedEvents {
		mergedEventStartTime, _ := time.Parse(time.RFC3339, mergedEvent.StartTime)
		if time.Now().Before(mergedEventStartTime) {
			n.UpcomingEvent = mergedEvent
			break
		}
	}

	if n.UpcomingEvent != nil {
		log.Printf("Upcoming event is: %q\n", n.UpcomingEvent.Summary)
	}
}

func (n *Notifier) syncCalendar() error {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).Format(time.RFC3339)
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.Local).Format(time.RFC3339)

	events, err := n.Service.Events.List("primary").TimeMin(startOfDay).TimeMax(endOfDay).Do()
	if err != nil {
		log.Printf("Error updating events. Error: %s\n", err.Error())
		return err
	}

	n.populateEvents(events)
	n.mergeOverlappingEvents()
	n.setUpcomingEvent()
	return nil
}

// watch is a blocking method that continuously monitors the current calendar state.
// It checks for calendar updates at midnight and tracks the status of the upcoming event,
// triggering actions when an event starts or ends. The method runs in an infinite loop
// and can be stopped by sending a signal to the done channel.
func (n *Notifier) watch() {
	for {
		select {
		case <-n.done:
			log.Println("stopping the ticker")
			return

		case <-n.t.C:
			// this takes care of refreshing the calendar post midnight
			if n.currentDay != time.Now().Day() {
				log.Println("syncing calendar post midnight")
				n.currentDay = time.Now().Day()
				err := n.syncCalendar()
				if err != nil {
					log.Println("error syncing calendar")
				}
			}

			if n.UpcomingEvent == nil {
				break
			}

			if n.UpcomingEvent.inProgress() {
				// light it up
				fmt.Println("light it up")
			} else if n.UpcomingEvent.hasEnded() {
				// lights off
				fmt.Println("lights off")
				n.setUpcomingEvent()
			}
		}
	}
}

// The controller
func (n *Notifier) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := n.syncCalendar()
	if err != nil {
		log.Println("error syncing calendar")
	}

	// TODO: check if any of the following is needed?
	// bodyBytes, err := io.ReadAll(r.Body)
	// if err != nil {
	// 	log.Println("Error reading request body")
	// 	return
	// }
	// // r.Body.Close()
	// r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	// bodyString := string(bodyBytes)
	// log.Printf("bodyString: %s", bodyString)
	// w.WriteHeader(http.StatusOK)
	// w.Write([]byte("200 OK"))
}
