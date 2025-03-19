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

type Notifier struct {
	bulbState                string
	Service                  *calendar.Service
	Events                   []*Event
	UpcomingEvent            *Event
	MergedEvents             []*Event
	EventNotificationChannel *calendar.Channel
	Wg                       *sync.WaitGroup
	t                        *time.Ticker
	hourTicker               *time.Ticker
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
		t:                        time.NewTicker(1 * time.Minute),
		hourTicker:               time.NewTicker(1 * time.Hour),
		done:                     make(chan struct{}),
		currentDay:               time.Now().Day(),
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

	for _, e := range n.Events {
		if e.IsRecurring {
			e.updateStartTimeForRecurringEvent()
		}
	}

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

		totalMergedEvents := len(n.MergedEvents)
		lastMergedEvent := n.MergedEvents[totalMergedEvents-1]

		if lastMergedEvent.partiallyOverlapsWith(currEvent) {
			e := &Event{
				Summary:     fmt.Sprintf("%s:%s", lastMergedEvent.Summary, currEvent.Summary),
				Description: fmt.Sprintf("%s:%s", lastMergedEvent.Description, currEvent.Description),
				StartTime:   lastMergedEvent.StartTime,
				EndTime:     currEvent.EndTime,
			}
			n.MergedEvents[totalMergedEvents-1] = e
		} else if lastMergedEvent.completelyOverlapsWith(currEvent) {

		} else {
			n.MergedEvents = append(n.MergedEvents, currEvent)
		}
	}

	log.Println("events in calendar")
	for _, me := range n.MergedEvents {
		log.Println(me.Summary)
	}

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

		log.Printf("\ncurrent time: %s\nmerged event start time: %s\nupcoming event start time: %s\n",
			currentTime.Format(time.TimeOnly),
			mergedEventStartTime.Format(time.TimeOnly),
			upcomingEventStartTime.Format(time.TimeOnly),
		)

		log.Println(currentTime.Before(mergedEventStartTime))
		log.Println(mergedEventStartTime.Before(upcomingEventStartTime))

		log.Println(mergedEventStartTime)
		log.Println(upcomingEventStartTime)

		if currentTime.Before(mergedEventStartTime) && mergedEventStartTime.Before(upcomingEventStartTime) {
			// now ---> mergedEvent ---> upcomingEvent
			// if the mergedEvent falls between `now` and `upcomingEvent`, then set upcomingEvent = mergedEvent
			n.UpcomingEvent = mergedEvent
		}
	}
	log.Printf("Upcoming event is: %q, starts at: %q\n", n.UpcomingEvent.Summary, n.UpcomingEvent.StartTime)
}

func (n *Notifier) fetchUpcomingEventStartTime() string {
	if n.UpcomingEvent.IsRecurring {

	}

	return n.UpcomingEvent.StartTime
}

func (n *Notifier) fetchUpcomingEventEndTime() string {
	if n.UpcomingEvent.IsRecurring {

	}

	return n.UpcomingEvent.EndTime
}

// TODO: Yet to test this part after recurring event bit is sorted
func (n *Notifier) upcomingEventStarted() bool {
	now := time.Now()
	st, _ := time.Parse(time.RFC3339, n.fetchUpcomingEventStartTime())
	et, _ := time.Parse(time.RFC3339, n.fetchUpcomingEventEndTime())

	log.Println("checking if upcoming event has started")
	log.Printf("start time: %s", st.Format(time.DateTime))
	log.Printf("end time: %s", et.Format(time.DateTime))
	log.Printf("now time: %s", now.Format(time.DateTime))

	if now.After(st) && now.Before(et) {
		return true
	}
	return false
}

func (n *Notifier) upcomingEventEnded() bool {
	now := time.Now()
	et, _ := time.Parse(time.RFC3339, n.fetchUpcomingEventEndTime())

	log.Println("checking if upcoming event has ended")
	log.Printf("end time: %s", et.Format(time.DateTime))
	log.Printf("now time: %s", now.Format(time.DateTime))

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
				n.UpcomingEvent = nil
			}

			if n.UpcomingEvent == nil {
				continue
			}

			if n.upcomingEventStarted() {
				log.Printf("Event %q in progress", n.UpcomingEvent.Summary)
				// This is where you notify subscribers - if there are any - that the next event has begun
				// and the light bulb must be on
				if n.bulbState == bulbStateOff {
					n.bulbState = bulbStateOn
				}
			}

			if n.upcomingEventEnded() {
				log.Printf("Event %q has ended", n.UpcomingEvent.Summary)
				// This is where you notify subscribers - if there are any - that the next event has ended
				// and the light bulb must be off
				n.bulbState = bulbStateOff
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
