package main

import (
	"os"
	"time"

	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
)

const (
	consumeInterval = 60 * time.Second
)

func main() {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logger.SetOutput(os.Stdout)
	logger.SetLevel(logrus.InfoLevel)

	docker, err := client.NewClientWithOpts()
	if err != nil {
		panic(err)
	}

	el := newEventLog()
	pm := newPM(logger, docker)
	c := newConsumer(logger, docker)

	go func() {
		for {
			time.Sleep(consumeInterval)
			c.consume(el)
		}
	}()

	pm.run(el)
}
