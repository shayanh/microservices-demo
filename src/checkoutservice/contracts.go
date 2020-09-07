package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"google.golang.org/grpc"
)

type ctxKey int

const (
	RequestIDKey ctxKey = iota + 1
)

type Condition interface{}

func invokeCondition(c Condition, req interface{}) error {
	v := reflect.ValueOf(c)
	t := v.Type()
	fmt.Println("numIn = ", t.NumIn())
	argv := make([]reflect.Value, t.NumIn())
	argv[0] = reflect.ValueOf(req)
	res := v.Call(argv)
	err, _ := res[0].Interface().(error)
	return err
}

type Method interface{}

func getMethodName(method Method) string {
	return runtime.FuncForPC(reflect.ValueOf(method).Pointer()).Name()
}

type UnaryRPCContract struct {
	Method         Method
	PreConditions  []Condition
	PostConditions []Condition
}

type Logger interface {
	Info(args ...interface{})
	Error(args ...interface{})
}

type UnaryRPCCall struct {
	MethodName string
	Request    interface{}
	Response   interface{}
	Error      error
}

type ServerContract struct {
	UnaryRPCContracts []UnaryRPCContract
	Logger            Logger

	callsLock     sync.RWMutex
	unaryRPCCalls map[string]map[string]UnaryRPCCall
}

func checkMethod(method Method, info *grpc.UnaryServerInfo) bool {
	tmp1 := strings.Split(getMethodName(method), ".")
	tmp2 := strings.Split(info.FullMethod, "/")
	m1 := tmp1[len(tmp1)-1]
	m2 := tmp2[len(tmp2)-1]
	return m1 == m2
}

func shortID() string {
	b := make([]byte, 10)
	io.ReadFull(rand.Reader, b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (sc *ServerContract) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		sc.Logger.Info("server info full method = ", info.FullMethod)

		requestID := shortID()
		ctx = context.WithValue(ctx, RequestIDKey, requestID)

		sc.Logger.Info("pre")
		for _, contract := range sc.UnaryRPCContracts {
			if eq := checkMethod(contract.Method, info); eq {
				sc.Logger.Info("contract method = ", getMethodName(contract.Method))
				for _, preCondition := range contract.PreConditions {
					err := invokeCondition(preCondition, req)
					if err != nil {
						sc.Logger.Error(err)
					}
				}
			}
		}

		resp, err := handler(ctx, req)

		sc.Logger.Info("post")
		for _, contract := range sc.UnaryRPCContracts {
			if eq := checkMethod(contract.Method, info); eq {
				sc.Logger.Info("contract method = ", getMethodName(contract.Method))
				for _, postCondition := range contract.PostConditions {
					err := invokeCondition(postCondition, resp)
					if err != nil {
						sc.Logger.Error(err)
					}
				}
			}
		}

		return resp, err
	}
}

func (sc *ServerContract) UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)

		requestID, ok := ctx.Value(RequestIDKey).(string)
		if ok {
			sc.callsLock.Lock()
			defer sc.callsLock.Unlock()

			call := UnaryRPCCall{
				MethodName: method,
				Request:    req,
				Response:   reply,
			}
			sc.unaryRPCCalls[requestID][method] = call
		}
		return err
	}
}
