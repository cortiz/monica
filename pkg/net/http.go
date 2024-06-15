package net

import (
	"io"
	"net/http"
	"strings"

	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/request"
)

func HTTPRequest(request *request.Request, headerOnly bool) *HTTPResponse {
	req, err := http.NewRequest(request.Method, request.URL, strings.NewReader(request.Body))
	if err != nil {
		logging.Log.Error().Err(err).Msg("Failed to create HTTP request")
	}
	for k, v := range request.Headers {
		req.Header.Add(k, v)
	}
	req.Header.Add("User-Agent", "Monica/0.1.0")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logging.Log.Error().Err(err).Msg("Failed to send HTTP request")
	}
	return parseHTTPResponse(resp, headerOnly)
}

func parseHTTPResponse(resp *http.Response, headerOnly bool) *HTTPResponse {
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
			logging.Log.Error().Err(err).Msg("Failed to read response body")
		}
		response.Body = string(bodyBytes)
	}
	return response
}

func parseResponseHeaders(headers http.Header) map[string]string {
	responseHeaders := make(map[string]string)
	for k, v := range headers {
		responseHeaders[k] = strings.Join(v, ",")
	}
	return responseHeaders
}
