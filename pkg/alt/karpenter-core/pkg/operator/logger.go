// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Unmodified copy of ignoreDebugEventsSink from
// https://github.com/aws/karpenter-core/blob/v0.30.0/pkg/operator/logger.go

package operator

import (
	"github.com/go-logr/logr"
)

type ignoreDebugEventsSink struct {
	name string
	sink logr.LogSink
}

func (i ignoreDebugEventsSink) Init(ri logr.RuntimeInfo) {
	i.sink.Init(ri)
}

func (i ignoreDebugEventsSink) Enabled(level int) bool { return i.sink.Enabled(level) }

func (i ignoreDebugEventsSink) Info(level int, msg string, keysAndValues ...interface{}) {
	// ignore debug "events" logs
	if level == 1 && i.name == "events" {
		return
	}
	i.sink.Info(level, msg, keysAndValues...)
}

func (i ignoreDebugEventsSink) Error(err error, msg string, keysAndValues ...interface{}) {
	i.sink.Error(err, msg, keysAndValues...)
}

func (i ignoreDebugEventsSink) WithValues(keysAndValues ...interface{}) logr.LogSink {
	return i.sink.WithValues(keysAndValues...)
}

func (i ignoreDebugEventsSink) WithName(name string) logr.LogSink {
	return &ignoreDebugEventsSink{name: name, sink: i.sink.WithName(name)}
}

func ignoreDebugEvents(logger logr.Logger) logr.Logger {
	return logr.New(&ignoreDebugEventsSink{sink: logger.GetSink()})
}
