package lab

import (
	"net/http"
	"net/http/httptest"
)

// synchronousHTTPClient runs a fixture handler through the ordinary HTTP client
// and response-decoding paths without external sockets. Its requests complete
// within a synctest bubble, so production timers can advance in virtual time.
func synchronousHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		response := recorder.Result()
		if request.Body != nil {
			if err := request.Body.Close(); err != nil {
				_ = response.Body.Close()
				return nil, err
			}
		}
		return response, nil
	})}
}
