package runner_test

import (
	"bytes"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/runner"
)

type reportRecorder struct {
	got     []report.Request
	summary *report.Summary
}

func (r *reportRecorder) Request(q report.Request) { r.got = append(r.got, q) }
func (r *reportRecorder) Done(s report.Summary) error {
	r.summary = &s
	return nil
}

func TestRunnerReports(t *testing.T) {
	srv, _ := apiServer(t)
	dead := closedURL(t)
	dir := t.TempDir()
	write(t, dir, "monica.yml", "version: 1\ndefaults:\n  vars:\n    key: ${KEY}\n    base: "+srv.URL+"\n")
	mon := write(t, dir, "api.mon", `# @name echo
GET {{base}}/echo?k={{key}}

> {%
  test("ok", () => expect(res.status).toBe(200));
  test("bad", () => expect(res.status).toBe(500));
%}

### down
GET `+dead+`/x

###
# @skip later
GET {{base}}/skipped
`)
	bad := write(t, dir, "bad.mon", "GET\n")

	rec := &reportRecorder{}
	opts := &runner.Opts{LookupEnv: func(n string) (string, bool) { return "s3cret", n == "KEY" }}
	r := runner.NewWithReporter(opts, rec)
	_ = r.RunFile(mon)
	_ = r.RunFile(bad)
	if err := r.Finish(); err != nil {
		t.Fatal(err)
	}

	if len(rec.got) != 4 {
		t.Fatalf("got %d results: %+v", len(rec.got), rec.got)
	}
	echo, down, skipped, file := rec.got[0], rec.got[1], rec.got[2], rec.got[3]

	if echo.Name != "echo" || echo.Line != 2 || echo.Status != 200 || echo.StatusText != "OK" || echo.Duration <= 0 {
		t.Errorf("echo: %+v", echo)
	}
	if echo.URL != srv.URL+"/echo?k=****" || strings.Contains(echo.Response, "s3cret") {
		t.Errorf("echo must be expanded and masked: url=%q", echo.URL)
	}
	if len(echo.Tests) != 2 || !echo.Tests[0].Passed || echo.Tests[1].Error != "expected 200 to be 500" || !echo.Failed() {
		t.Errorf("echo tests: %+v", echo.Tests)
	}
	if down.Name != "down" || down.Status != 0 || !strings.HasPrefix(down.Error, "sending HTTP request:") {
		t.Errorf("down: %+v", down)
	}
	if !skipped.Skipped || skipped.SkipReason != "later" {
		t.Errorf("skipped: %+v", skipped)
	}
	if file.Name != "bad.mon" || file.Line != 1 || !strings.Contains(file.Error, "invalid request line") {
		t.Errorf("file failure: %+v", file)
	}
	want := report.Summary{Requests: 4, Failed: 3, Skipped: 1, Tests: 2, TestsPassed: 1, TestsFailed: 1}
	got := *rec.summary
	got.Duration = 0
	if got != want {
		t.Errorf("summary = %+v, want %+v", got, want)
	}
}

func TestRunSingleFilePrintsConsoleReport(t *testing.T) {
	srv, _ := apiServer(t)
	var out bytes.Buffer
	mon := writeMon(t, "# @name ping\nGET "+srv.URL+"/echo\n> {% test(\"ok\", () => {}) %}\n")
	if err := runner.RunSingleFile(mon, &runner.Opts{Quiet: true}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"✓ ping  GET " + srv.URL + "/echo → 200 OK", "    ✓ ok\n", "1 request: 1 passed, 0 failed · 1 test: 1 passed, 0 failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "GET /echo x-pre") {
		t.Errorf("quiet output must not include the body:\n%s", got)
	}
}
