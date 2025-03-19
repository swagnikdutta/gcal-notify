### Todos
- Understand why channel is working?
- Add authorisation check to your webhook endpoint
- Should I look into the request body? In the webhook? If there's nothing to look for, clean that code.

### since i keep on forgetting

What the app does is, whenever it starts or some change occurs to the calendar.
It fetches all the events fresh, merges the overlapping events and saves what would be - the next upcoming event.

Every 10 second, a ticker will run and check if the upcoming event has started or ended. Accordingly, it will publish
appropriate messages.
