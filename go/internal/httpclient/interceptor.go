package httpclient

// Interceptor mirrors src/NzbDrone.Common/Http/IHttpRequestInterceptor.cs:1-8.
//
// PreRequest runs before the dispatcher dispatches; PostResponse runs after
// the dispatcher returns and before the client returns to the caller.
//
// Implementations registered with the client are called in the order
// supplied. Either method may return the original argument unchanged or a
// modified copy; PostResponse may also panic / return an error-wrapped
// response when it wants to fail the call (the CloudFlare interceptor does
// this in C# — Go interceptors should return an error via the response or
// just throw a normal error from the dispatcher chain in 1c).
type Interceptor interface {
	PreRequest(req *Request) *Request
	PostResponse(resp *Response) *Response
}

// InterceptorFunc adapts paired functions to the Interceptor interface for
// callers that don't want to define a struct.
type InterceptorFunc struct {
	Pre  func(*Request) *Request
	Post func(*Response) *Response
}

// PreRequest implements Interceptor.
func (f InterceptorFunc) PreRequest(req *Request) *Request {
	if f.Pre == nil {
		return req
	}
	return f.Pre(req)
}

// PostResponse implements Interceptor.
func (f InterceptorFunc) PostResponse(resp *Response) *Response {
	if f.Post == nil {
		return resp
	}
	return f.Post(resp)
}
