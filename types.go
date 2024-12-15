package main

import (
	"log"
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
	Service                  *calendar.Service
	Events                   []*Event
	MergedEvents             []*Event
	EventNotificationChannel *calendar.Channel
	Wg                       *sync.WaitGroup
}
