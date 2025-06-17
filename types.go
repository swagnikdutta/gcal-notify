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

func (e *Event) isYetToStart() bool {
	now := time.Now()
	if now.Before(e.StartTime) {
		return true
	}
	return false
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
	if now.After(e.EndTime) {
		return true
	}
	return false
}

type Throttler struct {
	Mu          sync.Mutex
	LastTrigger time.Time
	Interval    time.Duration
}

func NewThrottler(interval time.Duration) *Throttler {
	return &Throttler{
		Mu:          sync.Mutex{},
		LastTrigger: time.Time{},
		Interval:    interval,
	}
}

func (t *Throttler) Allow() bool {
	t.Mu.Lock()
	defer t.Mu.Unlock()

	now := time.Now()
	if t.LastTrigger.IsZero() || now.Sub(t.LastTrigger) >= t.Interval {
		t.LastTrigger = now
		return true
	}
	return false
}

func (t *Throttler) Reset() {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	t.LastTrigger = time.Time{}
}

type Notifier struct {
	calendarId string
	Service    *calendar.Service
	Events     []*Event
	// UpcomingEvent is either the currently in-progress event (if any), else the next immediate event that's coming up
	UpcomingEvent            *Event
	MergedEvents             []*Event
	EventNotificationChannel *calendar.Channel
	Wg                       *sync.WaitGroup
	t                        *time.Ticker
	done                     chan struct{}
	currentDay               int
	StartEventThrottler      *Throttler
	NilEventThrottler        *Throttler
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

	// fmt.Printf("\nlist of events in calendar(sorted by start time) - %d\n", len(n.Events))
	// for idx, e := range n.Events {
	// 	fmt.Printf("%d) %s\n", idx+1, e.Summary)
	// }

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

	// fmt.Printf("\nlist of merged events in calendar(sorted by start time) - %d\n", len(n.MergedEvents))
	// for idx, e := range n.MergedEvents {
	// 	fmt.Printf("%d) %s\n", idx+1, e.Summary)
	// }
	// fmt.Println()
}

func (n *Notifier) setUpcomingEvent() {
	n.UpcomingEvent = nil // set this to null once the most recent event ends

	for _, mergedEvent := range n.MergedEvents {
		now := time.Now()

		onGoingEvent := (now.After(mergedEvent.StartTime) || now.Equal(mergedEvent.StartTime)) &&
			(now.Before(mergedEvent.EndTime) || now.Equal(mergedEvent.EndTime))
		upComingEvent := now.Before(mergedEvent.StartTime)

		// if now >= start && now <=end
		if onGoingEvent || upComingEvent {
			n.UpcomingEvent = mergedEvent
			break
		}
	}

	if n.UpcomingEvent != nil {
		log.Printf("Upcoming event: %q, starting at: %q, ending at: %q\n", n.UpcomingEvent.Summary,
			n.UpcomingEvent.StartTime.Format(time.Kitchen), n.UpcomingEvent.EndTime.Format(time.Kitchen))
	}
}

func (n *Notifier) syncCalendar() error {
	// Reset throttle offsets
	n.NilEventThrottler.Reset()
	n.StartEventThrottler.Reset()

	log.Println("Syncing calendar...")
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
					continue
				}
			}

			if n.UpcomingEvent == nil {
				// Consider this situation. An ongoing event is dragged away, making it either a past event or an
				// upcoming event. In either case, the light must go off - which is why we are notifying the hue
				// agent to turn the bulb off. But, since this ticker will run at very short intervals (1 sec),
				// we don't want to flood hue-agent with "turn bulb off" requests.
				// Hence, we rate-limit.
				if n.NilEventThrottler.Allow() {
					n.notifyHueAgent(eventEnded)
				}
				continue
			}

			if n.UpcomingEvent.isYetToStart() && n.NilEventThrottler.Allow() {
				// Similar complicated use case as above.
				// If the event is dragged down (opposite to the above case), the bulb has to go off immediately, and
				// the n.UpcomingEvent won't be nil this time - hence the above if-block won't cover it.
				//
				// Also, it's safe to reuse the NilEventThrottler because, at any given time there are two
				// possibilities. Either UpcomingEvent will be nil or it won't. So there is no need for a
				// separate 'YetToStartThrottler' for this use case.
				n.notifyHueAgent(eventEnded)
				continue
			}

			if n.UpcomingEvent.inProgress() && n.StartEventThrottler.Allow() {
				log.Printf("Event %q started. Notifying hue agent: %q\n", n.UpcomingEvent.Summary, eventStarted)
				n.notifyHueAgent(eventStarted)
				continue
			}

			if n.UpcomingEvent.hasEnded() {
				log.Printf("Event %q ended. Notifying hue agent: %q\n", n.UpcomingEvent.Summary, eventEnded)
				n.notifyHueAgent(eventEnded)
				n.setUpcomingEvent()
			}
		}
	}
}

func (n *Notifier) handleCalendarUpdates(w http.ResponseWriter, r *http.Request) {
	// TODO: move this to a middleware
	if n.EventNotificationChannel.Id != r.Header.Get(googHeaderChannelId) {
		log.Printf("%s Forbidden operation. Channel id do not match or is missing from headers", http.StatusForbidden)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("403. Forbidden"))
		return
	}

	log.Println("Calendar updated")
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
		"on":         status == eventStarted,
		"mirek":      colorTemperature,
		"brightness": brightness,
	})

	req, err := http.NewRequest(http.MethodPost, requestUrl, bytes.NewBuffer(payloadBytes))
	if err != nil {
		log.Printf("Error creating HTTP request: %s\n", err)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Error connecting with hue agent: %s\n", err)
		return
	}
	defer res.Body.Close()

	bodyBytes, _ := io.ReadAll(res.Body)
	log.Printf("Successfully notified hue agent on %q. Status code: %v. Response: %v\n", status, res.StatusCode, string(bodyBytes))
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
		t:                        time.NewTicker(1 * time.Second),
		done:                     make(chan struct{}),
		currentDay:               time.Now().Day(),
		StartEventThrottler:      NewThrottler(NOTIFY_INTERVAL_ON_EVENT_START),
		NilEventThrottler:        NewThrottler(NOTIFY_INTERVAL_ON_NIL_EVENT),
	}
}
