package request

import (
	"bufio"
	"os"
	"strings"

	"jmpeax.com/sec/monica/internal/logging"
)

// ParseRequest parses a raw HTTP request and returns a Request object.
func ParseRequest(raw string) *Request {
	logging.Log.Debug().Msg("Parsing request")
	request := Request{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			logging.Log.Trace().Msgf("Skipping comment: %s", line)
			continue
		}
		if request.Method == "" {
			logging.Log.Trace().Msgf("Parsing request line: %s", line)
			parseRequestLine(line, &request)
			continue
		}
		if request.Headers == nil {
			request.Headers = make(map[string]string)
			for line != "" {
				logging.Log.Trace().Msgf("Parsing request headers: %s", line)
				parseRequestHeader(line, &request)
				if !scanner.Scan() {
					break // EOF
				}
				line = scanner.Text()
			}
			continue
		}
		request.Body = strings.Join([]string{request.Body, line}, "\n")
	}
	return &request
}

func ParseMonFile(file string) *Request {
	logging.Log.Debug().Msgf("Parsing file: %s", file)
	monFile, err := os.ReadFile(file)
	if err != nil {
		logging.Log.Error().Err(err).Msgf("Error reading file: %s", file)
		return nil
	}
	return ParseRequest(string(monFile))
}

func parseRequestHeader(line string, r *Request) {
	parts := strings.Split(line, ": ")
	if len(parts) == 2 {
		r.Headers[parts[0]] = parts[1]
	}
}

func parseRequestLine(requestLine string, r *Request) {
	parts := strings.Split(requestLine, " ")
	if len(parts) >= 2 {
		r.Method = parts[0]
		r.URL = parts[1]
	}
}
