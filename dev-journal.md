### Todos
- Understand why channel is working?
- Add authorisation check to your webhook endpoint
- reduce or minify logs
- Investigate the crash upon event deletion - log the error - cancelled event check
- Sometimes the channel might not be closed successfully - simulate the scenario and fix.
- Find out what's the upcoming event is set to after the last event of the day?

### since i keep on forgetting

What the app does is, whenever it starts or some change occurs to the calendar.
It fetches all the events fresh, merges the overlapping events and saves what would be - the next upcoming event.

Every one minute, a ticker will run and it will check if the upcoming event has started or not. 

