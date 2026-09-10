package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
)

const maxOAuthBodyBytes = 1 << 20

func readJSONResponse(response *http.Response, value any) ([]byte, error) {
	if response.Request != nil {
		log.Printf("[oauth] outbound response method=%s url=%s status=%d", response.Request.Method, response.Request.URL, response.StatusCode)
	} else {
		log.Printf("[oauth] outbound response status=%d", response.StatusCode)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOAuthBodyBytes+1))
	if err != nil || len(body) > maxOAuthBodyBytes {
		return nil, errors.New("oauth response is too large or unreadable")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var detail struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &detail)
		if detail.Error != "" {
			return nil, fmt.Errorf("oauth request failed: %s", detail.Error)
		}
		return nil, fmt.Errorf("oauth request failed with status %d", response.StatusCode)
	}
	if value != nil && len(body) > 0 {
		if err := json.Unmarshal(body, value); err != nil {
			return nil, err
		}
	}
	return body, nil
}
