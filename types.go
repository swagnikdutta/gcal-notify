package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"google.golang.org/api/calendar/v3"
)

type EventStatus string

type Event struct {
	Summary     string
	Description string
	StartTime   time.Time
	EndTime     time.Time
	IsRecurring bool
}

func (e *Event) updateStartTimeForRecurringEvent() {
	now := time.Now()

	// updatedStartTime := time.Date(
	// 	now.Year(), now.Month(), now.Day(),
	// 	e.StartTime.Hour(), e.StartTime.Minute(), e.StartTime.Second(), e.StartTime.Nanosecond(), e.StartTime.Location(),
	// )
	e.StartTime = time.Date(
		now.Year(), now.Month(), now.Day(),
		e.StartTime.Hour(), e.StartTime.Minute(), e.StartTime.Second(), e.StartTime.Nanosecond(), e.StartTime.Location(),
	)
	// I am not doing the same thing for end times because
	// 1) They don't matter in sorting or in any of my logic
	// 2) I need to decide what to do if end time spans over midnight and goes on to the next day.
}

func (e1 *Event) partiallyOverlapsWith(e2 *Event) bool {
	return (e1.EndTime.Equal(e2.StartTime) || e1.EndTime.After(e2.StartTime)) && e1.EndTime.Before(e2.EndTime)
}

func (e1 *Event) completelyOverlapsWith(e2 *Event) bool {
	return e1.EndTime.Equal(e2.EndTime) || e1.EndTime.After(e2.EndTime)
}

func (e *Event) inProgress() bool {
	now := time.Now()
	if (now.Equal(e.StartTime) || now.After(e.StartTime)) && (now.Equal(e.EndTime) || now.Before(e.EndTime)) {
		return true
	}
	return false
}

func (e *Event) hasEnded() bool {
	now := time.Now()
	endTime := e.EndTime

	if now.After(endTime) {
		return true
	}
	return false
}

type Notifier struct {
	calendarId               string
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

func (n *Notifier) populateEvents(events *calendar.Events) {
	n.Events = nil
	for _, ei := range events.Items {
		// TODO: read the documentation for the statuses
		if ei.Status == "cancelled" {
			continue
		}

		stp, _ := time.Parse(time.RFC3339, ei.Start.DateTime)
		etp, _ := time.Parse(time.RFC3339, ei.End.DateTime)
		e := &Event{
			Summary:     ei.Summary,
			Description: ei.Description,
			StartTime:   stp,
			EndTime:     etp,
			IsRecurring: ei.Recurrence != nil,
		}
		n.Events = append(n.Events, e)
	}

	for _, e := range n.Events {
		if e.IsRecurring {
			e.updateStartTimeForRecurringEvent()
		}
	}
}

func (n *Notifier) mergeOverlappingEvents() {
	slices.SortFunc(n.Events, func(e1, e2 *Event) int {
		return e1.StartTime.Compare(e2.StartTime)
	})

	fmt.Printf("\nlist of events in calendar(sorted by start time) - %d\n", len(n.Events))
	for idx, e := range n.Events {
		fmt.Printf("%d) %s\n", idx+1, e.Summary)
	}

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
		if time.Now().Before(mergedEvent.StartTime) {
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

	events, err := n.Service.Events.List(n.calendarId).TimeMin(startOfDay).TimeMax(endOfDay).Do()
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
					log.Println("error syncing calendar post midnight")
					break
				}
			}

			if n.UpcomingEvent == nil {
				break
			}

			if n.UpcomingEvent.inProgress() {
				retries := 2
				log.Printf("Event %q started. Notifying subscribers...", n.UpcomingEvent.Summary)
				startTime := n.UpcomingEvent.StartTime
				delta := int(time.Now().Sub(startTime).Seconds())

				fmt.Printf("delta: %d, 2*watchInterval: %d\n", delta, retries*watchInterval)

				if int(delta) <= retries*watchInterval {
					fmt.Println("Firing")
					n.notifyHueAgent(eventStarted)
				}

			} else if n.UpcomingEvent.hasEnded() {
				log.Printf("Event %q ended. Notifying subscribers...", n.UpcomingEvent.Summary)
				n.notifyHueAgent(eventEnded)
				n.setUpcomingEvent()
			}
		}
	}
}

func (n *Notifier) handleCalendarUpdates(w http.ResponseWriter, r *http.Request) {
	if n.EventNotificationChannel.Id != r.Header.Get(googHeaderChannelId) {
		log.Printf("%s Forbidden operation. Channel id do not match or is missing from headers", http.StatusForbidden)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("403. Forbidden"))
		return
	}

	err := n.syncCalendar()
	if err != nil {
		log.Println("error syncing calendar")
	}
	w.WriteHeader(http.StatusOK)
}

func (n *Notifier) healthCheck(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (n *Notifier) notifyHueAgent(status EventStatus) {
	requestUrl := fmt.Sprintf("%s/light/state", os.Getenv(hueAgentBaseUrl))
	payloadBytes, _ := json.Marshal(map[string]interface{}{
		"on": status == eventStarted,
	})

	req, err := http.NewRequest(http.MethodPost, requestUrl, bytes.NewBuffer(payloadBytes))
	if err != nil {
		log.Printf("Error creating HTTP request: %s\n", err)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Error occurred while notifying subscribers: %s\n", err)
	}
	defer res.Body.Close()

	bodyBytes, _ := io.ReadAll(res.Body)
	log.Printf("Successfully notified subscribers. Statuscode: %v\n", res.StatusCode)
	log.Printf(string(bodyBytes))
}

func NewNotifier() *Notifier {
	return &Notifier{
		calendarId:               os.Getenv(calendarId),
		Service:                  authenticate(),
		Events:                   make([]*Event, 0),
		MergedEvents:             make([]*Event, 0),
		UpcomingEvent:            nil,
		EventNotificationChannel: nil,
		Wg:                       &sync.WaitGroup{},
		t:                        time.NewTicker(time.Duration(watchInterval) * time.Second),
		done:                     make(chan struct{}),
		currentDay:               time.Now().Day(),
	}
}
