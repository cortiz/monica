package net

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/request"
)

func HTTPRequest(request *request.Request, headerOnly bool) (*HTTPResponse, error) {
	if request == nil {
		return nil, errors.New("nil request")
	}
	req, err := http.NewRequest(request.Method, request.URL, strings.NewReader(request.Body))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	for k, v := range request.Headers {
		req.Header.Add(k, v)
	}
	req.Header.Add("User-Agent", "Monica/0.1.0")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending HTTP request: %w", err)
	}
	return parseHTTPResponse(resp, headerOnly)
}

func parseHTTPResponse(resp *http.Response, headerOnly bool) (*HTTPResponse, error) {
	response := &HTTPResponse{
		Status:     resp.Proto + " " + resp.Status,
		StatusCode: resp.StatusCode,
		Headers:    parseResponseHeaders(resp.Header),
	}
	if !headerOnly {
		defer func(Body io.ReadCloser) {
			err := Body.Close()
			if err != nil {
				logging.Log.Error().Err(err).Msg("Failed to close response body")
			}
		}(resp.Body)
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading response body: %w", err)
		}
		response.Body = string(bodyBytes)
	}
	return response, nil
}

func parseResponseHeaders(headers http.Header) map[string]string {
	responseHeaders := make(map[string]string)
	for k, v := range headers {
		responseHeaders[k] = strings.Join(v, ",")
	}
	return responseHeaders
}
