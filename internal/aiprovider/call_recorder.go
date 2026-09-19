package aiprovider

import (
	"context"
	"net/http"
)

type callRecorderKey struct{}

// WithAPICallRecorder attaches a recorder for admin-side upstream queries
// (quota / models). Gateway Handle still takes the recorder as a direct argument.
func WithAPICallRecorder(ctx context.Context, recorder APICallRecorder) context.Context {
	if ctx == nil || recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, callRecorderKey{}, recorder)
}

func apiCallRecorder(ctx context.Context) APICallRecorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(callRecorderKey{}).(APICallRecorder)
	return recorder
}

func reportAPICall(ctx context.Context, trace *AIProviderCallTrace) {
	if trace == nil {
		return
	}
	if recorder := apiCallRecorder(ctx); recorder != nil {
		recorder(trace)
	}
}

// httpExchangeTrace builds a call trace for one upstream HTTP exchange used by
// quota and model listing. Transport failures leave ResponseStatus at 0.
func httpExchangeTrace(req *http.Request, resp *http.Response, body []byte, transportErr bool) *AIProviderCallTrace {
	trace := &AIProviderCallTrace{}
	if req != nil {
		if req.URL != nil {
			trace.URL = req.URL.String()
		}
		trace.OutboundRequestHeaders = req.Header.Clone()
	}
	if transportErr {
		trace.HTTPErrorInfo = "upstream request failed"
		return trace
	}
	if resp != nil {
		trace.ResponseStatus = resp.StatusCode
		trace.ResponseHeaders = resp.Header.Clone()
	}
	if len(body) > 0 {
		trace.ResponseBody = append([]byte(nil), body...)
	}
	return trace
}
