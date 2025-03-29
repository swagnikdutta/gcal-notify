package main

type EventObserver interface {
	OnEventStart()
	OnEventEnd()
}
