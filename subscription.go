package gqlengine

import (
	"context"
	"fmt"
	"reflect"

	"github.com/karfield/graphql"
)

type Subscription interface {
	SendData(data interface{}) error
	Close() error
}

type SubscriptionSession interface {
	GraphQLSubscriptionSession()
}

var (
	subscriptionType        = reflect.TypeOf((*Subscription)(nil)).Elem()
	subscriptionSessionType = reflect.TypeOf((*SubscriptionSession)(nil)).Elem()
)

func (s *subscriptionFeedback) SendData(data interface{}) error {
	d, err := unwrap(reflect.TypeOf(data))
	if err != nil {
		return fmt.Errorf("send %s only please", s.result.implType)
	}
	if d.baseType != s.result.baseType {
		return fmt.Errorf("send %s only not %s", s.result.baseType, d.baseType)
	}
	if d.array == s.result.array {
		if s.result.array {
			return fmt.Errorf("requires []%s", s.result.implType)
		}
		return fmt.Errorf("requires %s not []%s", s.result.implType, s.result.implType)
	}
	return s.send(data)
}

func (s *subscriptionFeedback) Close() error {
	s.close()
	return nil
}

type subscriptionArgBuilder struct{}

func (s *subscriptionArgBuilder) build(p graphql.ResolveParams) (reflect.Value, error) {
	feedback := p.Context.Value(wsCtxKey{})
	return reflect.ValueOf(feedback), nil
}

func (engine *Engine) asSubscriptionArg(p reflect.Type) (*subscriptionArgBuilder, error) {
	if p == subscriptionType {
		return &subscriptionArgBuilder{}, nil
	}
	isSub, _, err := implementsOf(p, subscriptionType)
	if err != nil {
		return nil, err
	}
	if !isSub {
		return nil, nil
	}
	return &subscriptionArgBuilder{}, nil
}

type subscriptionHandler struct {
	args             graphql.FieldConfigArgument
	result           graphql.Type
	onSubArgs        []resolverArgumentBuilder
	onSubscribedFn   reflect.Value
	onUnsubscribedFn *reflect.Value
	errIdx           int
	sessionResultIdx int // index of onSubscribed()'s SubscriptionSession result
	sessionArgIdx    int // index of onUnsubscribed()'s SubscriptionSession Argument
}

func (engine *Engine) checkSubscriptionHandler(onSubscribed, onUnsubscribed interface{}) (*subscriptionHandler, error) {
	h := subscriptionHandler{
		errIdx:           -1,
		sessionResultIdx: -1,
		sessionArgIdx:    -1,
	}
	subFnType := reflect.TypeOf(onSubscribed)
	if subFnType.Kind() != reflect.Func {
		return nil, fmt.Errorf("onSubscribed is not a function")
	}
	h.onSubArgs = make([]resolverArgumentBuilder, subFnType.NumIn())
	for i := 0; i < subFnType.NumIn(); i++ {
		in := subFnType.In(i)
		if argsBuilder, info, err := engine.asArguments(in); argsBuilder != nil || err != nil {
			if err != nil {
				return nil, err
			}
			if h.args != nil {
				return nil, fmt.Errorf("more than one arguments object at onSubscribed() arg[%d]: %s", i, in.String())
			}
			h.onSubArgs[i] = argsBuilder
			h.args, err = engine.collectFieldArgumentConfig(info.baseType)
			if err != nil {
				return nil, fmt.Errorf("illegal onSubscribed() arguments(%s) object in argument[%d]", in.String(), i)
			}
		} else if ctxBuilder, err := engine.asContextArgument(in); err != nil || ctxBuilder != nil {
			if err != nil {
				return nil, err
			}
			h.onSubArgs[i] = ctxBuilder
		} else if subBuilder, err := engine.asSubscriptionArg(in); err != nil || subBuilder != nil {
			if err != nil {
				return nil, err
			}
			h.onSubArgs[i] = subBuilder
		} else {
			return nil, fmt.Errorf("unsupported onSubscribed() argument type [%d]: '%s'", i, in)
		}
	}

	for i := 0; i < subFnType.NumOut(); i++ {
		out := subFnType.Out(i)
		if isSession, _, err := implementsOf(out, subscriptionSessionType); err != nil {
			return nil, err
		} else if isSession {
			if h.errIdx >= 0 {
				return nil, fmt.Errorf("more than one SubscriptionSession result of onSubscribed(): %s", out)
			}
			h.sessionResultIdx = i
		} else if engine.asErrorResult(out) {
			if h.errIdx >= 0 {
				return nil, fmt.Errorf("more than one error result of onSubscribed(): %s", out)
			}
			h.errIdx = i
		} else {
			return nil, fmt.Errorf("unsupported onSubscribed result[%d] %s", i, out.String())
		}
	}

	h.onSubscribedFn = reflect.ValueOf(onSubscribed)

	// check onUnsubscribed()
	if onUnsubscribed != nil {
		unsubFnType := reflect.TypeOf(onUnsubscribed)
		if unsubFnType.Kind() != reflect.Func {
			return nil, fmt.Errorf("onUnsubscribed is not a function")
		}

		for i := 0; i < unsubFnType.NumIn(); i++ {
			in := unsubFnType.In(i)
			if isSession, _, err := implementsOf(in, subscriptionSessionType); err != nil {
				return nil, err
			} else if isSession {
				if h.sessionArgIdx >= 0 {
					return nil, fmt.Errorf("more than one SubscriptionSession argument in onUnsubscribed()")
				}
				h.sessionArgIdx = i
			} else {
				return nil, fmt.Errorf("unsupported onUnsubscribed() argument[%d] %s", i, in)
			}
		}

		if h.sessionArgIdx >= 0 && h.sessionResultIdx < 0 {
			return nil, fmt.Errorf("onUnsubscribed() requires a SubscriptionSession which should returned by onSubscribed() but not")
		}
		fn := reflect.ValueOf(onUnsubscribed)
		h.onUnsubscribedFn = &fn
	}
	return &h, nil
}

func (h *subscriptionHandler) resolve(p graphql.ResolveParams) (interface{}, context.Context, error) {
	args := make([]reflect.Value, len(h.onSubArgs))
	if len(h.onSubArgs) > 0 {
		for i, arg := range h.onSubArgs {
			a, err := arg.build(p)
			if err != nil {
				return nil, p.Context, err
			}
			args[i] = a
		}
	}
	results := h.onSubscribedFn.Call(args)
	var err error
	if h.errIdx >= 0 {
		errVal := results[h.errIdx]
		if !errVal.IsNil() {
			err = errVal.Interface().(error)
		}
	}

	var session reflect.Value
	if h.sessionResultIdx >= 0 {
		session = results[h.sessionResultIdx]
	}

	return nil, context.WithValue(p.Context, subSetupCtxKey{}, &subInitResult{
		err: err,
		finalize: func() {
			if h.onUnsubscribedFn != nil {
				if nArgs := h.onUnsubscribedFn.Type().NumIn(); nArgs > 0 {
					args := make([]reflect.Value, nArgs)
					args[h.sessionArgIdx] = session
					h.onUnsubscribedFn.Call(args)
				} else {
					h.onUnsubscribedFn.Call([]reflect.Value{})
				}
			}
		},
	}), nil
}
