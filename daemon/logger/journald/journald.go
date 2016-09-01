// +build linux

// Package journald provides the log driver for forwarding server logs
// to endpoints that receive the systemd format.
package journald

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/coreos/go-systemd/journal"
	"github.com/docker/docker/daemon/logger"
	"github.com/docker/docker/daemon/logger/loggerutils"
)

const name = "journald"

type journald struct {
	vars    map[string]string // additional variables and values to send to the journal along with the log message
	eVars   map[string]string // vars, plus an extra one saying DOCKER_EVENT=true
	readers readerList
}

type readerList struct {
	mu      sync.Mutex
	readers map[*logger.LogWatcher]*logger.LogWatcher
}

func init() {
	if err := logger.RegisterLogDriver(name, New); err != nil {
		logrus.Fatal(err)
	}
	if err := logger.RegisterLogOptValidator(name, validateLogOpt); err != nil {
		logrus.Fatal(err)
	}
}

// New creates a journald logger using the configuration passed in on
// the context.
func New(ctx logger.Context) (logger.Logger, error) {
	if !journal.Enabled() {
		return nil, fmt.Errorf("journald is not enabled on this host")
	}
	// Strip a leading slash so that people can search for
	// CONTAINER_NAME=foo rather than CONTAINER_NAME=/foo.
	name := ctx.ContainerName
	if name[0] == '/' {
		name = name[1:]
	}

	// parse log tag
	tag, err := loggerutils.ParseLogTag(ctx, "")
	if err != nil {
		return nil, err
	}

	vars := map[string]string{
		"CONTAINER_ID":      ctx.ContainerID[:12],
		"CONTAINER_ID_FULL": ctx.ContainerID,
		"CONTAINER_NAME":    name,
		"CONTAINER_TAG":     tag,
	}
	extraAttrs := ctx.ExtraAttributes(strings.ToTitle)
	for k, v := range extraAttrs {
		vars[k] = v
	}

	eVars := map[string]string{"DOCKER_EVENT": "true"}
	for k, v := range vars {
		eVars[k] = v
	}
	return &journald{vars: vars, eVars: eVars, readers: readerList{readers: make(map[*logger.LogWatcher]*logger.LogWatcher)}}, nil
}

// We don't actually accept any options, but we have to supply a callback for
// the factory to pass the (probably empty) configuration map to.
func validateLogOpt(cfg map[string]string) error {
	for key := range cfg {
		switch key {
		case "labels":
		case "env":
		case "tag":
		default:
			return fmt.Errorf("unknown log opt '%s' for journald log driver", key)
		}
	}
	return nil
}

func (s *journald) Log(msg *logger.Message) error {
	if msg.Source == "event" {
		// Galaxy-specific change! If this is an "event" (container start or stop),
		// send it with the special DOCKER_EVENT=true field. Also, use a distinct
		// priority level from stdout/stderr, since different priority levels are
		// rate limited separately and we don't want a spammy container to cause
		// journald to drop the stop message.
		// https://github.com/systemd/systemd/blob/e5e0cffce784b2cf6f57f110cc9c4355f7703200/src/journal/journald-rate-limit.c#L39-L42
		return journal.Send(string(msg.Line), journal.PriWarning, s.eVars)
	}
	if msg.Source == "stderr" {
		return journal.Send(string(msg.Line), journal.PriErr, s.vars)
	}
	return journal.Send(string(msg.Line), journal.PriInfo, s.vars)
}

func (s *journald) Name() string {
	return name
}
