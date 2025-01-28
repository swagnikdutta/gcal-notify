- [DONE] ensure channel is working
- Understand why channel is working?
- [DONE] fetch all events, store them locally
- [DONE] merge intervals
- [DONE] read values from env file
- [DONE] Close the channel on server shutdown
- Add authorisation check to your webhook endpoint
- [DONE] create upcoming event
- [DONE] sort fetched events
- reduce or minify logs
- ticker logic to check for running events
- restart at midnight everyday
- update upcoming event
- Investigate the crash upon event deletion - log the error - cancelled event check
- Sometimes the channel might not be closed successfully - simulate the scenario and fix.

### since i keep on forgetting

What the app does is, whenever it starts or some change occurs to the calendar.
It fetches all the events fresh, merges the overlapping events and saves what would be - the next upcoming event.

Every one minute, a ticker will run and it will check if the upcoming event has started or not. 

