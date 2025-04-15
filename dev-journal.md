### Todos
- [DONE] add auth check in webhook route - put a check on channel id value
- [DONE] inspect request body, if we need to write back any response - sending back status code is enough.
- [DONE] change channel id
- auto-renew notification channel, post expiry. Currently there is no way to "renew" a channel, you would have to create a new one with a new id (unique value)
- [DONE] you need to return a success status code else google will retry with exponential back off

### since i keep on forgetting

What the app does is, whenever it starts or some change occurs to the calendar.
It fetches all the events fresh, merges the overlapping events and saves what would be - the next upcoming event.

Every 10 second, a ticker will run and check if the upcoming event has started or ended. Accordingly, it will publish
appropriate messages.

# bullet

so application runs in local container, as in fetches events but fails since webhook endpoint is missing.
i need to transfer credentials.json to the container.
but it is a secret, and containers should not have secret files within them like that. 
guideline says, to use workload identity federation - if not using google cloud services.

I ran the container using bind mount - mounted the .env file and credentials.json file
objective was to see if the secret files are present in the container or not - which is discouraged.
And yes they were present. And the application was running with those secrets.

how do i create .env file?
Maybe I can have a shell script within the repo and run it to create the .env files. It can just do cp .env.example.
But how do I put values in the env files?
