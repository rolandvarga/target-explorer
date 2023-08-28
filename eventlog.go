package main

import (
	"sync"
	"time"
)

type eventType int

const (
	startEvent eventType = iota + 1
	runningEvent
	stopEvent
	dieEvent
)

var eventTable = map[string]eventType{
	"start":   startEvent,
	"running": runningEvent,
	"stop":    stopEvent,
	"die":     dieEvent,
}

type event struct {
	action      eventType
	containerID string
	name        string
	recordedAt  time.Time
}

type eventLog struct {
	mu     sync.Mutex
	events []event
}

func newEventLog() *eventLog {
	return &eventLog{
		mu:     sync.Mutex{},
		events: make([]event, 0),
	}
}

func (el *eventLog) push(e event) {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.events = append(el.events, e)
}

func (el *eventLog) flush() []event {
	el.mu.Lock()
	defer el.mu.Unlock()

	out := make([]event, len(el.events))
	copy(out, el.events)
	el.events = nil

	return out
}
