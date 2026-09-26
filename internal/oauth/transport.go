package oauth

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"ai-unisub/internal/database"
	"ai-unisub/internal/proxy"
)

type proxyTransport struct {
	manager proxy.ProxyManager
	groupID int
	app     string
}

type recordingTransport struct {
	base http.RoundTripper
	db   database.Database
	app  string
}

func (t *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, requestErr := t.base.RoundTrip(request)
	status := 0
	message := ""
	if response != nil {
		status = response.StatusCode
	}
	if requestErr != nil {
		message = requestErr.Error()
	}
	logErr := t.db.RecordProxyLog(&database.PersistedProxyLog{
		URL: request.URL.String(), AppType: t.app, HTTPErrorCode: status,
		HTTPErrorMessage: message, Time: time.Now(),
	})
	return response, errors.Join(requestErr, logErr)
}

func (t *proxyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, errRequestNil
	}
	var body []byte
	if request.Body != nil {
		var err error
		body, err = io.ReadAll(request.Body)
		closeErr := request.Body.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return t.manager.Do(t.groupID, t.app, request, body, func(response *http.Response) error {
		if response.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("oauth upstream returned status %d", response.StatusCode)
		}
		return nil
	})
}
