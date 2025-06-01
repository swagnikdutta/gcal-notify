package main

import "time"

const (
	googHeaderChannelId       string        = "X-Goog-Channel-Id"
	channelTypeWebhook        string        = "web_hook"
	eventStarted              EventStatus   = "event_started"
	eventEnded                EventStatus   = "event_ended"
	hueAgentRateLimitInterval time.Duration = 10

	// related to env file variables
	notificationChannelEndpoint = "NOTIFICATION_CHANNEL_ENDPOINT"
	calendarId                  = "CALENDAR_ID"
	hueAgentBaseUrl             = "HUE_AGENT_BASE_URL"
)
