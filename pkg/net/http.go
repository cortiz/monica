package net

import (
	"net/http"
	"strings"

	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/request"
)

func HTTPRequest(request *request.Request) *HTTPResponse {
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
	return parseHTTPResponse(resp)
}

func parseHTTPResponse(resp *http.Response) *HTTPResponse {
	response := &HTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    parseResponseHeaders(resp.Header),
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
