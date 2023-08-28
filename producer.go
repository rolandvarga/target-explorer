package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
)

var (
	ErrProducerReceiveEvent = fmt.Errorf("producer receiving event")
	ErrProducerParseLabel   = fmt.Errorf("producer parsing label")
)

type producer interface {
	produceEventsFor(*eventLog)
}

type producerType int

const (
	scraper producerType = iota + 1
	eventStreamer
)

type producerManager struct {
	producers map[producerType]producer
}

func newPM(logger *logrus.Logger, docker *client.Client) producerManager {
	producers := make(map[producerType]producer)

	producers[scraper] = scraperImpl{logger, docker}
	producers[eventStreamer] = eventStreamerImpl{logger, docker}

	return producerManager{producers: producers}
}

func (pm producerManager) run(el *eventLog) {
	for p := scraper; p < eventStreamer+1; p++ {
		pm.producers[p].produceEventsFor(el)
	}
}

type scraperImpl struct {
	logger *logrus.Logger
	docker *client.Client
}

func (s scraperImpl) produceEventsFor(el *eventLog) {
	containers, err := s.docker.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		s.logger.Errorf("%v: %s", ErrProducerReceiveEvent, err)
	}

	for _, container := range containers {
		if label, ok := container.Labels["scrape_target"]; ok {
			isTarget, err := strconv.ParseBool(label)
			if err != nil {
				s.logger.Errorf("%v: %s", ErrProducerParseLabel, err)
			}

			if isTarget {
				el.push(event{
					action:      runningEvent,
					containerID: container.ID,
					name:        container.Names[0],
					recordedAt:  time.Now(),
				})
			}
		}
	}
}

type eventStreamerImpl struct {
	logger *logrus.Logger
	docker *client.Client
}

func (es eventStreamerImpl) produceEventsFor(el *eventLog) {
	msgEvents, errEvents := es.docker.Events(context.Background(), types.EventsOptions{
		Filters: filters.NewArgs(
			filters.Arg("type", "container"),
			filters.Arg("event", "start"),
			filters.Arg("event", "stop"),
			filters.Arg("event", "die"),
			filters.Arg("label", "scrape_target=true"),
		),
	})

	for {
		select {
		case msg := <-msgEvents:
			el.push(event{
				action:      eventTable[msg.Action],
				containerID: msg.Actor.ID,
				name:        msg.Actor.Attributes["com.docker.compose.service"],
				recordedAt:  time.Now(),
			})
		case err := <-errEvents:
			es.logger.Errorf("%v: %s", ErrProducerReceiveEvent, err)
		}
	}
}
