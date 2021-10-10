// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package types

type (
	// SecurityEvent is a generic security event payload holding an actual security event (eg. a WAF security event),
	// along with its optional context.
	SecurityEvent struct {
		Event   interface{}
		Context []SecurityEventContext
	}

	// SecurityEventContext is the interface implemented by security event contexts, which can be attached to
	// security events to add more run-time context to them.
	SecurityEventContext interface {
		isSecurityEventContext()
	}
)

// NewSecurityEvent returns a new security event along with the provided context.
func NewSecurityEvent(event interface{}, ctx ...SecurityEventContext) *SecurityEvent {
	return &SecurityEvent{
		Event:   event,
		Context: ctx,
	}
}

// AddContext allows adding extra security event contexts to an already created security event.
func (e *SecurityEvent) AddContext(ctx ...SecurityEventContext) {
	e.Context = append(e.Context, ctx...)
}

type (
	// HTTPContext is the security event context describing an HTTP handler. It includes information about its
	// request and response.
	HTTPContext struct {
		Request  HTTPRequestContext
		Response HTTPResponseContext
	}

	// HTTPRequestContext is the HTTP request context of an HTTP operation context.
	HTTPRequestContext struct {
		Method     string
		Host       string
		IsTLS      bool
		RequestURI string
		RemoteAddr string
		Path       string
		Headers    map[string][]string
		Query      map[string][]string
	}

	// HTTPResponseContext is the HTTP response context of an HTTP operation context.
	HTTPResponseContext struct {
		Status  int
		Headers map[string][]string
	}
)

func (HTTPContext) isSecurityEventContext() {}

// SpanContext is the APM span context. It allows to provide the span and its trace IDs where the security event
// happened.
type SpanContext struct {
	TraceID, SpanID uint64
}

func (SpanContext) isSecurityEventContext() {}

// ServiceContext is the running service context.
type ServiceContext struct {
	Name, Version, Environment string
}

func (ServiceContext) isSecurityEventContext() {}

// TagContext is the slide of user-defined tags.
type TagContext []string

func (TagContext) isSecurityEventContext() {}

// TracerContext is the APM tracer context.
type TracerContext struct {
	Runtime, RuntimeVersion, Version string
}

func (TracerContext) isSecurityEventContext() {}

// HostContext is the running host context.
type HostContext struct {
	Hostname, OS string
}

func (HostContext) isSecurityEventContext() {}