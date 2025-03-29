### Todos
- add auth check in webhook route
  - check channel id value - [DONE]
- inspect request body, if we need to write back any response - sending back status code is enough [DONE]
- change channel id
- auto-renew notification channel, post expiry.
  - currently there is no way to "renew" a channel, you would have to create a new one with a new id (unique value)
- you need to return a success status code else google will retry with exponential back off. [DONE]


### Thoughts

the notification message is supposed to describe the change in the resource.

### since i keep on forgetting

What the app does is, whenever it starts or some change occurs to the calendar.
It fetches all the events fresh, merges the overlapping events and saves what would be - the next upcoming event.

Every 10 second, a ticker will run and check if the upcoming event has started or ended. Accordingly, it will publish
appropriate messages.
