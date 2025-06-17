package main

import "time"

const (
	googHeaderChannelId string      = "X-Goog-Channel-Id"
	channelTypeWebhook  string      = "web_hook"
	eventStarted        EventStatus = "event_started"
	eventEnded          EventStatus = "event_ended"
	colorTemperature    int         = 200
	brightness          int         = 100

	// related to rate limiting
	NOTIFY_INTERVAL_ON_EVENT_START time.Duration = 1 * time.Minute
	NOTIFY_INTERVAL_ON_NIL_EVENT   time.Duration = 5 * time.Minute

	// related to env file variables
	notificationChannelEndpoint = "NOTIFICATION_CHANNEL_ENDPOINT"
	calendarId                  = "CALENDAR_ID"
	hueAgentBaseUrl             = "HUE_AGENT_BASE_URL"
)
