package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"

	"gopkg.in/yaml.v2"
)

var (
	ErrConsumerInspectContainer = fmt.Errorf("consumer inspecting container")
	ErrConsumerGetCurrentState  = fmt.Errorf("consumer getting current state")
	ErrConsumerParseHostMapping = fmt.Errorf("consumer parsing host mapping")
	ErrConsumerDiffTargets      = fmt.Errorf("consumer diffing targets")
	ErrConsumerPublish          = fmt.Errorf("consumer publishing scrape targets")
	ErrConsumerSendSignal       = fmt.Errorf("consumer sending signal")
	ErrConsumerNewRequest       = fmt.Errorf("consumer creating new request")
	ErrConsumerMakeRequest      = fmt.Errorf("consumer making request")
)

const (
	prometheusConfigPath = "prometheus-local/prometheus.yaml"
	dockerHostAddress    = "host.docker.internal"
	metricsPort          = "2112/tcp"

	globalScrapeInterval = "60s"
	reloadEndpoint       = "http://localhost:9090/-/reload"
)

type consumer struct {
	logger *logrus.Logger
	docker *client.Client
}

func newConsumer(logger *logrus.Logger, docker *client.Client) consumer {
	return consumer{logger, docker}
}

func (c consumer) consume(el *eventLog) {
	events := el.flush()
	if len(events) == 0 {
		return
	}

	filteredEvents := c.applyEventFilter(events)

	stateMap, err := c.getCurrentState()
	if err != nil {
		c.logger.Errorf("%v: %s", ErrConsumerGetCurrentState, err)
		return
	}

	scrapeTargets := c.diff(filteredEvents, stateMap)
	err = c.publish(scrapeTargets)
	if err != nil {
		c.logger.Errorf("%v: %s", ErrConsumerPublish, err)
	}

	err = c.sendSignal()
	if err != nil {
		c.logger.Errorf("%v: %s", ErrConsumerSendSignal, err)
	}
}

func (c consumer) applyEventFilter(events []event) map[string]event {
	filteredEvents := make(map[string]event, 0)

	for _, event := range events {
		filteredEvents[event.containerID] = event
	}
	return filteredEvents
}

type prometheusConf struct {
	Global struct {
		ScrapeInterval string `yaml:"scrape_interval"`
	} `yaml:"global"`
	ScrapeConfigs []struct {
		JobName       string `yaml:"job_name"`
		StaticConfigs []struct {
			Targets []string `yaml:"targets"`
		} `yaml:"static_configs"`
	} `yaml:"scrape_configs"`
}

func (c consumer) getCurrentState() (map[string]string, error) {
	stateMap := make(map[string]string, 0)

	f, err := os.ReadFile(prometheusConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return stateMap, nil
		}
		return nil, err
	}

	var prometheusConf prometheusConf

	err = yaml.Unmarshal(f, &prometheusConf)
	if err != nil {
		return nil, err
	}

	for _, scrapeConfig := range prometheusConf.ScrapeConfigs {
		stateMap[scrapeConfig.JobName] = scrapeConfig.StaticConfigs[0].Targets[0]
	}
	return stateMap, nil
}

func (c consumer) diff(events map[string]event, stateMap map[string]string) map[string]string {
	for _, event := range events {
		switch event.action {
		case startEvent, runningEvent:
			hostMapping, err := c.lookupHostMappingFor(event.containerID)
			if err != nil {
				c.logger.Errorf("%v: %s", ErrConsumerDiffTargets, err)
				continue
			}
			stateMap[event.name] = hostMapping
		case stopEvent, dieEvent:
			delete(stateMap, event.containerID)
		}
	}
	return stateMap
}

func (c consumer) lookupHostMappingFor(container string) (string, error) {
	ctx, timeout := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer timeout()

	inspect, err := c.docker.ContainerInspect(ctx, container)
	if err != nil {
		return "", fmt.Errorf("%v: %s", ErrConsumerInspectContainer, err)
	}

	if hostMapping, ok := inspect.NetworkSettings.Ports[metricsPort]; ok {
		return fmt.Sprintf("%s:%s", dockerHostAddress, hostMapping[0].HostPort), nil
	}
	return "", fmt.Errorf("%v: port 2112 not present", ErrConsumerParseHostMapping)
}

func (c consumer) publish(scrapeTargets map[string]string) error {
	var promConf prometheusConf
	promConf.Global.ScrapeInterval = globalScrapeInterval

	for jobName, target := range scrapeTargets {
		promConf.ScrapeConfigs = append(promConf.ScrapeConfigs, struct {
			JobName       string `yaml:"job_name"`
			StaticConfigs []struct {
				Targets []string `yaml:"targets"`
			} `yaml:"static_configs"`
		}{
			JobName: jobName,
			StaticConfigs: []struct {
				Targets []string `yaml:"targets"`
			}{
				{
					Targets: []string{target},
				},
			},
		})
	}

	f, err := os.OpenFile(prometheusConfigPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("%v: %s", ErrConsumerPublish, err)
	}

	enc := yaml.NewEncoder(f)
	err = enc.Encode(promConf)
	if err != nil {
		return fmt.Errorf("%v: %s", ErrConsumerPublish, err)
	}
	return nil
}

func (c consumer) sendSignal() error {
	client := http.Client{Timeout: 500 * time.Millisecond}
	req, err := http.NewRequest("POST", reloadEndpoint, nil)
	if err != nil {
		return fmt.Errorf("%v: %s", ErrConsumerNewRequest, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%v: %s", ErrConsumerMakeRequest, err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%v: %s", ErrConsumerMakeRequest, resp.Status)
	}

	c.logger.Print("sent reload signal to prometheus")
	return nil
}
