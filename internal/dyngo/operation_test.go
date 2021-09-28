// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package dyngo_test

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/dyngo"

	"github.com/stretchr/testify/require"
)

// Dummy struct to mimic real-life operation stacks.
type (
	RootArgs        struct{}
	RootRes         struct{}
	HTTPHandlerArgs struct {
		URL     *url.URL
		Headers http.Header
	}
	HTTPHandlerRes struct{}
	SQLQueryArg    struct {
		Query string
	}
	SQLQueryResult struct {
		Err error
	}
	GRPCHandlerArg struct {
		Msg interface{}
	}
	GRPCHandlerResult struct {
		Res interface{}
	}
	JSONParserArg struct {
		Buf []byte
	}
	JSONParserResults struct {
		Value interface{}
		Err   error
	}
	BodyReadArg struct{}
	BodyReadRes struct {
		Buf []byte
		Err error
	}
	RawBodyData []byte
)

func init() {
	dyngo.RegisterOperation((*RootArgs)(nil), (*RootRes)(nil))
	dyngo.RegisterOperation((*HTTPHandlerArgs)(nil), (*HTTPHandlerRes)(nil))
	dyngo.RegisterOperation((*SQLQueryArg)(nil), (*SQLQueryResult)(nil))
	dyngo.RegisterOperation((*GRPCHandlerArg)(nil), (*GRPCHandlerResult)(nil))
	dyngo.RegisterOperation((*JSONParserArg)(nil), (*JSONParserResults)(nil))
	dyngo.RegisterOperation((*BodyReadArg)(nil), (*BodyReadRes)(nil))
}

func TestUsage(t *testing.T) {
	t.Run("operation stacking", func(t *testing.T) {
		// Dummy waf looking for the string `attack` in HTTPHandlerArgs
		wafListener := func(called *int, blocked *bool) dyngo.EventListener {
			return dyngo.OnStartEventListener((*HTTPHandlerArgs)(nil), func(op *dyngo.Operation, v interface{}) {
				args := v.(HTTPHandlerArgs)
				*called++

				if strings.Contains(args.URL.RawQuery, "attack") {
					*blocked = true
					return
				}
				for _, values := range args.Headers {
					for _, v := range values {
						if strings.Contains(v, "attack") {
							*blocked = true
							return
						}
					}
				}

				op.OnData((*RawBodyData)(nil), func(_ *dyngo.Operation, v interface{}) {
					body := v.(RawBodyData)
					if strings.Contains(string(body), "attack") {
						*blocked = true
					}
				})
			})
		}

		// HTTP body read listener appending the read results to a buffer
		rawBodyListener := func(called *int, buf *[]byte) dyngo.EventListener {
			return dyngo.OnStartEventListener((*HTTPHandlerArgs)(nil), func(op *dyngo.Operation, _ interface{}) {
				op.OnFinish((*BodyReadRes)(nil), func(op *dyngo.Operation, v interface{}) {
					res := v.(BodyReadRes)
					*called++
					*buf = append(*buf, res.Buf...)
					if res.Err == io.EOF {
						op.EmitData(RawBodyData(*buf))
					}
				})
			})
		}

		jsonBodyValueListener := func(called *int, value *interface{}) dyngo.EventListener {
			return dyngo.OnStartEventListener((*HTTPHandlerArgs)(nil), func(op *dyngo.Operation, _ interface{}) {
				didBodyRead := false
				op.OnFinish((*JSONParserResults)(nil), func(op *dyngo.Operation, v interface{}) {
					res := v.(JSONParserResults)
					*called++
					if !didBodyRead || res.Err != nil {
						return
					}
					*value = res.Value
				})
				op.OnStart((*BodyReadArg)(nil), func(_ *dyngo.Operation, _ interface{}) {
					didBodyRead = true
				})
			})
		}

		t.Run("stack monitored and not blocked by waf", func(t *testing.T) {
			root := dyngo.StartOperation(RootArgs{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			operation(
				root,
				HTTPHandlerArgs{
					URL:     &url.URL{RawQuery: "?v=ok"},
					Headers: http.Header{"header": []string{"value"}}},
				HTTPHandlerRes{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: []interface{}{"a", "json", "array"}}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("my ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("bo")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("dy"), Err: io.EOF}, nil)
					})
					operation(op, SQLQueryArg{}, SQLQueryResult{}, nil)
				},
			)

			// WAF callback called without blocking
			require.False(t, WAFBlocked)
			require.Equal(t, 1, WAFCalled)

			// The raw body listener has been called
			require.Equal(t, []byte("my raw body"), RawBodyBuf)
			require.Equal(t, 4, RawBodyCalled)

			// The json body value listener has been called
			require.Equal(t, 1, JSONBodyParserCalled)
			require.Equal(t, []interface{}{"a", "json", "array"}, JSONBodyParserValue)
		})

		t.Run("stack monitored and blocked by waf via the http operation monitoring", func(t *testing.T) {
			root := dyngo.StartOperation(RootArgs{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			RawBodyBuf = nil
			operation(
				root,
				HTTPHandlerArgs{
					URL:     &url.URL{RawQuery: "?v=attack"},
					Headers: http.Header{"header": []string{"value"}}},
				HTTPHandlerRes{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: "a string", Err: errors.New("an error")}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("another ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("bo")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("dy"), Err: nil}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte(" value"), Err: io.EOF}, nil)
					})

					operation(op, SQLQueryArg{}, SQLQueryResult{}, nil)
				},
			)

			// WAF callback called and blocked
			require.True(t, WAFBlocked)
			require.Equal(t, 1, WAFCalled)

			// The raw body listener has been called
			require.Equal(t, 5, RawBodyCalled)
			require.Equal(t, []byte("another raw body value"), RawBodyBuf)

			// The json body value listener has been called but no value due to a parser error
			require.Equal(t, 1, JSONBodyParserCalled)
			require.Equal(t, nil, JSONBodyParserValue)
		})

		t.Run("stack monitored and blocked by waf via the raw body monitoring", func(t *testing.T) {
			root := dyngo.StartOperation(RootArgs{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			RawBodyBuf = nil
			operation(
				root,
				HTTPHandlerArgs{
					URL:     &url.URL{RawQuery: "?v=ok"},
					Headers: http.Header{"header": []string{"value"}}},
				HTTPHandlerRes{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: "a string"}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("an ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("att")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("a")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("ck"), Err: io.EOF}, nil)
					})

					operation(op, SQLQueryArg{}, SQLQueryResult{}, nil)
				},
			)

			// WAF callback called and blocked
			require.True(t, WAFBlocked)
			require.Equal(t, 1, WAFCalled)

			// The raw body listener has been called
			require.Equal(t, 4, RawBodyCalled)
			require.Equal(t, []byte("an attack"), RawBodyBuf)

			// The json body value listener has been called but no value due to a parser error
			require.Equal(t, 1, JSONBodyParserCalled)
			require.Equal(t, "a string", JSONBodyParserValue)
		})

		t.Run("stack not monitored", func(t *testing.T) {
			root := dyngo.StartOperation(RootArgs{})

			var (
				WAFBlocked bool
				WAFCalled  int
			)
			wafListener := wafListener(&WAFCalled, &WAFBlocked)

			var (
				RawBodyBuf    []byte
				RawBodyCalled int
			)
			rawBodyListener := rawBodyListener(&RawBodyCalled, &RawBodyBuf)

			var (
				JSONBodyParserValue  interface{}
				JSONBodyParserCalled int
			)
			jsonBodyValueListener := jsonBodyValueListener(&JSONBodyParserCalled, &JSONBodyParserValue)

			root.Register(rawBodyListener, wafListener, jsonBodyValueListener)

			// Run the monitored stack of operations
			operation(
				root,
				GRPCHandlerArg{}, GRPCHandlerResult{},
				func(op *dyngo.Operation) {
					operation(op, JSONParserArg{}, JSONParserResults{Value: []interface{}{"a", "json", "array"}}, func(op *dyngo.Operation) {
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("my ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("raw ")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("bo")}, nil)
						operation(op, BodyReadArg{}, BodyReadRes{Buf: []byte("dy"), Err: io.EOF}, nil)
					})
					operation(op, SQLQueryArg{}, SQLQueryResult{}, nil)
				},
			)

			// WAF callback called without blocking
			require.False(t, WAFBlocked)
			require.Equal(t, 0, WAFCalled)

			// The raw body listener has been called
			require.Nil(t, RawBodyBuf)
			require.Equal(t, 0, RawBodyCalled)

			// The json body value listener has been called
			require.Equal(t, 0, JSONBodyParserCalled)
			require.Nil(t, JSONBodyParserValue)
		})
	})

	t.Run("recursive operation", func(t *testing.T) {
		root := dyngo.StartOperation(RootArgs{})
		defer root.Finish(RootRes{})

		called := 0
		root.OnStart((*HTTPHandlerArgs)(nil), func(_ *dyngo.Operation, _ interface{}) {
			called++
		})

		operation(root, HTTPHandlerArgs{}, HTTPHandlerRes{}, func(o *dyngo.Operation) {
			operation(o, HTTPHandlerArgs{}, HTTPHandlerRes{}, func(o *dyngo.Operation) {
				operation(o, HTTPHandlerArgs{}, HTTPHandlerRes{}, func(o *dyngo.Operation) {
					operation(o, HTTPHandlerArgs{}, HTTPHandlerRes{}, func(o *dyngo.Operation) {
						operation(o, HTTPHandlerArgs{}, HTTPHandlerRes{}, func(*dyngo.Operation) {
						})
					})
				})
			})
		})

		require.Equal(t, 5, called)
	})
}

type (
	MyOperationArgs struct{}
	MyOperationData struct{}
	MyOperationRes  struct{}
)

func init() {
	dyngo.RegisterOperation((*MyOperationArgs)(nil), (*MyOperationRes)(nil))
}

func TestRegisterUnregister(t *testing.T) {
	// TODO(julio): test unregistering one event
	// TODO(julio): test unregistering several times
	var onStartCalled, onDataCalled, onFinishCalled int

	unregister := dyngo.Register(
		dyngo.InstrumentationDescriptor{
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnStartEventListener((*MyOperationArgs)(nil), func(*dyngo.Operation, interface{}) {
					onStartCalled++
				}),
			},
		},
		dyngo.InstrumentationDescriptor{
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnStartEventListener((*MyOperationArgs)(nil), func(*dyngo.Operation, interface{}) {
					onDataCalled++
				}),
			},
		},
		dyngo.InstrumentationDescriptor{
			Instrumentation: dyngo.OperationInstrumentation{
				EventListener: dyngo.OnFinishEventListener((*MyOperationRes)(nil), func(*dyngo.Operation, interface{}) {
					onFinishCalled++
				}),
			},
		},
	)

	operation(nil, MyOperationArgs{}, MyOperationRes{}, func(op *dyngo.Operation) {
		op.EmitData(MyOperationData{})
	})
	require.Equal(t, 1, onStartCalled)
	require.Equal(t, 1, onDataCalled)
	require.Equal(t, 1, onFinishCalled)

	unregister()
	operation(nil, MyOperationArgs{}, MyOperationRes{}, func(op *dyngo.Operation) {
		op.EmitData(MyOperationData{})
	})
	require.Equal(t, 1, onStartCalled)
	require.Equal(t, 1, onDataCalled)
	require.Equal(t, 1, onFinishCalled)

}

func TestTypeSafety(t *testing.T) {
	t.Run("nil start arguments", func(t *testing.T) {
		require.Panics(t, func() {
			dyngo.StartOperation(nil)
		})
	})

	t.Run("nil finish results", func(t *testing.T) {
		require.Panics(t, func() {
			op := dyngo.StartOperation(MyOperationArgs{})
			op.Finish(nil)
		})
	})

	t.Run("invalid operation types", func(t *testing.T) {
		type (
			myOpArg struct{}
			myOpRes struct{}
		)

		require.Panics(t, func() {
			dyngo.RegisterOperation(nil, nil)
		})
		require.Panics(t, func() {
			dyngo.RegisterOperation(myOpArg{}, nil)
		})
		require.Panics(t, func() {
			dyngo.RegisterOperation(nil, myOpRes{})
		})
		require.Panics(t, func() {
			dyngo.RegisterOperation("not ok", myOpRes{})
		})
		require.Panics(t, func() {
			dyngo.RegisterOperation(myOpArg{}, "not ok")
		})
		type myInterface interface{}
		require.Panics(t, func() {
			dyngo.RegisterOperation(myOpArg{}, myInterface(nil))
		})
		require.Panics(t, func() {
			dyngo.RegisterOperation(myInterface(nil), myOpRes{})
		})
	})

	t.Run("multiple operation registration", func(t *testing.T) {
		type (
			myOp1Arg struct{}
			myOp1Res struct{}
		)

		require.NotPanics(t, func() {
			dyngo.RegisterOperation((*myOp1Arg)(nil), (*myOp1Res)(nil))
		})
		require.Panics(t, func() {
			// Already registered
			dyngo.RegisterOperation((*myOp1Arg)(nil), (*myOp1Res)(nil))
		})
	})

	t.Run("operation usage before registration", func(t *testing.T) {
		type (
			myOp2Arg struct{}
			myOp2Res struct{}

			myOp3Arg struct{}
			myOp3Res struct{}
		)

		// Start a not yet registered operation
		require.Panics(t, func() {
			dyngo.StartOperation(myOp2Arg{})
		})

		dyngo.RegisterOperation((*myOp2Arg)(nil), (*myOp2Res)(nil))

		t.Run("finishing with the expected result type", func(t *testing.T) {
			require.NotPanics(t, func() {
				op := dyngo.StartOperation(myOp2Arg{})
				// Finish with the expected result type
				op.Finish(myOp2Res{})
			})
		})

		t.Run("finishing with the wrong operation result type", func(t *testing.T) {
			require.Panics(t, func() {
				op := dyngo.StartOperation(myOp2Arg{})
				// Finish with the wrong result type
				op.Finish(&myOp2Res{})
			})
		})

		t.Run("starting an operation with the wrong operation argument type", func(t *testing.T) {
			require.Panics(t, func() {
				// Start with the wrong argument type
				dyngo.StartOperation(myOp2Res{})
			})
		})

		t.Run("listening to an operation not yet registered", func(t *testing.T) {
			require.Panics(t, func() {
				op := dyngo.StartOperation(myOp2Arg{})
				defer op.Finish(myOp2Res{})
				op.OnStart((*myOp3Arg)(nil), func(*dyngo.Operation, interface{}) {})
			})
			require.Panics(t, func() {
				op := dyngo.StartOperation(myOp2Arg{})
				defer op.Finish(myOp2Res{})
				op.OnFinish((*myOp3Res)(nil), func(*dyngo.Operation, interface{}) {})
			})
			require.NotPanics(t, func() {
				dyngo.RegisterOperation((*myOp3Arg)(nil), (*myOp3Res)(nil))
				op := dyngo.StartOperation(myOp2Arg{})
				defer op.Finish(myOp2Res{})
				op.OnStart((*myOp3Arg)(nil), func(*dyngo.Operation, interface{}) {})
				op.OnFinish((*myOp3Res)(nil), func(*dyngo.Operation, interface{}) {})
			})
		})
	})

	t.Run("event listeners", func(t *testing.T) {
		type (
			myOp4Arg  struct{}
			myOp4Res  struct{}
			myOp4Data struct{}
		)

		dyngo.RegisterOperation((*myOp4Arg)(nil), (*myOp4Res)(nil))

		op := dyngo.StartOperation(myOp4Arg{})
		defer op.Finish(myOp4Res{})

		t.Run("valid event key type", func(t *testing.T) {
			require.NotPanics(t, func() {
				op.OnStart((*myOp4Arg)(nil), func(*dyngo.Operation, interface{}) {})
				op.OnFinish((*myOp4Res)(nil), func(*dyngo.Operation, interface{}) {})
				op.OnData((*myOp4Data)(nil), func(*dyngo.Operation, interface{}) {})
			})
		})

		t.Run("invalid event key type", func(t *testing.T) {
			require.Panics(t, func() {
				op.OnStart(nil, func(*dyngo.Operation, interface{}) {})
			})
			require.Panics(t, func() {
				op.OnStart(myOp4Arg{}, func(*dyngo.Operation, interface{}) {})
			})
			require.Panics(t, func() {
				op.OnStart((**myOp4Arg)(nil), func(*dyngo.Operation, interface{}) {})
			})

			require.Panics(t, func() {
				op.OnFinish(nil, func(*dyngo.Operation, interface{}) {})
			})
			require.Panics(t, func() {
				op.OnFinish(myOp4Res{}, func(*dyngo.Operation, interface{}) {})
			})
			require.Panics(t, func() {
				op.OnFinish((**myOp4Res)(nil), func(*dyngo.Operation, interface{}) {})
			})

			require.Panics(t, func() {
				op.OnData(nil, func(*dyngo.Operation, interface{}) {})
			})
			require.Panics(t, func() {
				op.OnData(myOp4Data{}, func(*dyngo.Operation, interface{}) {})
			})
		})
	})
}

// TODO(julio): concurrency test

func operation(parent *dyngo.Operation, args, res interface{}, child func(*dyngo.Operation)) {
	op := dyngo.StartOperation(args, dyngo.WithParent(parent))
	defer op.Finish(res)
	if child != nil {
		child(op)
	}
}