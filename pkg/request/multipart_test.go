package request_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/request"
)

func TestParseMultipartFile(t *testing.T) {
	reqs, err := request.ParseMonFile("testdata/multipart.mon")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 {
		t.Fatalf("got %d requests", len(reqs))
	}
	up := reqs[0]
	wantParts := []request.BodyPart{
		{Text: "--MonicaBoundary\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nUsers for {{env}}\r\n" +
			"--MonicaBoundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"users.csv\"\r\nContent-Type: text/csv\r\n\r\n"},
		{Text: "id,name\n1,ada\n", File: filepath.Join("testdata", "payloads", "users.csv")},
		{Text: "\r\n--MonicaBoundary--\r\n"},
	}
	if len(up.Parts) != len(wantParts) {
		t.Fatalf("parts = %#v", up.Parts)
	}
	for i := range wantParts {
		if up.Parts[i] != wantParts[i] {
			t.Errorf("part %d = %#v\nwant %#v", i, up.Parts[i], wantParts[i])
		}
	}
	if up.Body != wantParts[0].Text+wantParts[1].Text+wantParts[2].Text {
		t.Errorf("body = %q", up.Body)
	}

	// Render expands the text parts only.
	out, err := up.Render(func(s string) (string, error) {
		return strings.ReplaceAll(strings.ReplaceAll(s, "{{env}}", "dev"), "ada", "XXX"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Body, "Users for dev\r\n") || !strings.Contains(out.Body, "1,ada\n") {
		t.Errorf("rendered body = %q", out.Body)
	}

	if raw := reqs[1]; raw.Parts != nil || raw.Body != "--MonicaBoundary\n< ./payloads/users.csv\n" {
		t.Errorf("non-multipart request: parts %v, body %q", raw.Parts, raw.Body)
	}
}

func TestParseMultipart(t *testing.T) {
	const head = "POST http://h/u\nContent-Type: multipart/form-data; boundary=\"b\"\n\n"
	tests := []struct {
		name     string
		raw      string
		wantBody string
		wantErr  string
	}{
		{"crlf source, quoted boundary, trailing spaces on delimiters",
			strings.ReplaceAll(head+"--b  \nContent-Disposition: form-data; name=\"a\"\n\nx\ny\n--b--  \n\n", "\n", "\r\n"),
			"--b\r\nContent-Disposition: form-data; name=\"a\"\r\n\r\nx\r\ny\r\n--b--\r\n", ""},
		{"empty part content and leading blank lines",
			head + "\n\n--b\nContent-Disposition: form-data; name=\"a\"\n\n--b--",
			"--b\r\nContent-Disposition: form-data; name=\"a\"\r\n\r\n\r\n--b--\r\n", ""},
		{"multi-line content that looks like a file ref stays text",
			head + "--b\nX: 1\n\n< ./x\nmore\n--b--\n",
			"--b\r\nX: 1\r\n\r\n< ./x\r\nmore\r\n--b--\r\n", ""},
		{"no body is fine", head, "", ""},
		{"templated content type is not parsed",
			"POST http://h/u\nContent-Type: {{ct}}\n\n--b\n", "--b\n", ""},
		{"no boundary", "POST http://h/u\nContent-Type: multipart/form-data\n\n--b\n", "", "line 1: multipart/form-data body needs a boundary"},
		{"missing opening", head + "hello\n--b--\n", "", "line 4: multipart body must start with a \"--b\" line"},
		{"missing closing", head + "--b\nX: 1\n\nv\n", "", "line 4: multipart body has no closing \"--b--\" line"},
		{"no blank line after part headers", head + "--b\nX: 1\n--b--\n", "", "line 4: multipart part needs a blank line"},
		{"text after closing", head + "--b\nX: 1\n\nv\n--b--\nextra\n", "", "line 9: unexpected text after the closing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqs, err := request.ParseRequest(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if reqs[0].Body != tt.wantBody {
				t.Errorf("body = %q\nwant   %q", reqs[0].Body, tt.wantBody)
			}
		})
	}
}

func TestParseMultipartFileErrors(t *testing.T) {
	tests := []struct{ file, want string }{
		{"testdata/multipart_no_close.mon", "multipart_no_close.mon:4: multipart body has no closing"},
		{"testdata/multipart_missing_file.mon", "multipart_missing_file.mon:7: reading multipart file"},
	}
	for _, tt := range tests {
		_, err := request.ParseMonFile(tt.file)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.file, err, tt.want)
		}
		if tt.file == "testdata/multipart_missing_file.mon" && err != nil {
			var pe *request.ParseError
			if !errors.As(err, &pe) || pe.Err == nil || !os.IsNotExist(pe.Err) {
				t.Errorf("%s: underlying error = %v", tt.file, err)
			}
		}
	}
}
