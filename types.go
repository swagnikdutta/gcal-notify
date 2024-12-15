package main

import (
	"sync"

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
	Wg                       *sync.WaitGroup
}
