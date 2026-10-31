package script

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
)

// runExpect runs one expect() statement and returns the assertion message
// ("" when it passed).
func runExpect(t *testing.T, code string) string {
	t.Helper()
	e, _ := newTestEngine()
	results, err := e.run(src(`test("t", () => { `+code+` })`), nil, 0)
	if err != nil {
		t.Fatalf("%s: script error: %v", code, err)
	}
	if len(results) != 1 {
		t.Fatalf("%s: got %d results", code, len(results))
	}
	return results[0].Error
}

func TestMatchers(t *testing.T) {
	tests := []struct {
		code    string
		wantErr string // "" = passes
	}{
		{`expect(200).toBe(200)`, ""},
		{`expect(404).toBe(200)`, "expected 404 to be 200"},
		{`expect("a").toBe("a")`, ""},
		{`expect({}).toBe({})`, "expected {} to be {}"},
		{`expect(NaN).toBe(NaN)`, ""},
		{`expect(1).not.toBe(2)`, ""},
		{`expect(1).not.toBe(1)`, "expected 1 not to be 1"},

		{`expect({a: [1, {b: "x"}]}).toEqual({a: [1, {b: "x"}]})`, ""},
		{`expect([1, 2]).toEqual([1, 2, 3])`, "expected values to be deeply equal\n  expected: [1,2,3]\n  received: [1,2]"},
		{`expect({a: 1, b: 2}).toEqual({b: 2, a: 1})`, ""},
		{`expect({a: 1}).toEqual({a: 1, b: undefined})`, "deeply equal"},
		{`expect([1]).toEqual({0: 1})`, "deeply equal"},
		{`expect("1").toEqual(1)`, "deeply equal"},
		{`expect({a: 1}).not.toEqual({a: 2})`, ""},

		{`expect("hello world").toContain("o w")`, ""},
		{`expect([1, {id: 2}]).toContain({id: 2})`, ""},
		{`expect([1, 2]).toContain(3)`, "expected [1,2] to contain 3"},
		{`expect("abc").not.toContain("z")`, ""},

		{`expect("user-42").toMatch(/^user-\d+$/)`, ""},
		{`expect("user-x").toMatch(/^user-\d+$/)`, `expected "user-x" to match /^user-\d+$/`},
		{`expect("application/json; charset=utf-8").toMatch("json")`, ""},

		{`expect(120).toBeLessThan(500)`, ""},
		{`expect(500).toBeLessThan(500)`, "expected 500 to be less than 500"},
		{`expect(500).toBeLessThanOrEqual(500)`, ""},
		{`expect(3).toBeGreaterThan(2)`, ""},
		{`expect(2).toBeGreaterThanOrEqual(3)`, "expected 2 to be greater than or equal to 3"},
		{`expect("5").toBeLessThan(10)`, "but both must be numbers"},

		{`expect({user: {id: 7}}).toHaveProperty("user.id")`, ""},
		{`expect({user: {id: 7}}).toHaveProperty("user.id", 7)`, ""},
		{`expect({user: {id: 7}}).toHaveProperty("user.id", 8)`, `to have property "user.id" equal to 8, got 7`},
		{`expect({list: ["a"]}).toHaveProperty("list.0", "a")`, ""},
		{`expect({a: 1}).toHaveProperty("b")`, `expected {"a":1} to have property "b"`},
		{`expect({a: 1}).not.toHaveProperty("b")`, ""},

		{`expect([1, 2, 3]).toHaveLength(3)`, ""},
		{`expect("abc").toHaveLength(3)`, ""},
		{`expect(null).toHaveLength(0)`, "expected null to have length 0"},

		{`expect(1).toBeTruthy()`, ""},
		{`expect("").toBeTruthy()`, `expected "" to be truthy`},
		{`expect(0).toBeFalsy()`, ""},
		{`expect(null).toBeNull()`, ""},
		{`expect(undefined).toBeUndefined()`, ""},
		{`expect(0).toBeDefined()`, ""},
		{`expect(undefined).toBeDefined()`, "expected undefined to be defined"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			got := runExpect(t, tt.code)
			if tt.wantErr == "" && got != "" {
				t.Fatalf("expected pass, got %q", got)
			}
			if tt.wantErr != "" && !strings.Contains(got, tt.wantErr) {
				t.Fatalf("failure %q does not contain %q", got, tt.wantErr)
			}
		})
	}
}

func TestTestRecordsEveryOutcome(t *testing.T) {
	e, _ := newTestEngine()
	res := &net.HTTPResponse{StatusCode: 404, Body: `{"name": "ada"}`, Headers: http.Header{}, Duration: time.Millisecond}
	req := &request.Request{Method: "GET", URL: "http://a.example/", Headers: http.Header{}}
	code := `
		test("status is 200", () => expect(res.status).toBe(200));
		test("name", () => { expect(res.json().name).toEqual("ada"); });
		test("crash", () => { null.x; });
		test("thrown string", () => { throw "plain"; });
		console.log("after tests");
	`
	results, err := e.RunPost(src(code), req, res, Context{Vars: mapVars{}})
	if err != nil {
		t.Fatal(err)
	}
	want := []TestResult{
		{Name: "status is 200", Error: "expected 404 to be 200"},
		{Name: "name", Passed: true},
		{Name: "crash", Error: "TypeError: Cannot read property 'x' of undefined"}, // goja's wording for null
		{Name: "thrown string", Error: "plain"},
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("got  %+v\nwant %+v", results, want)
	}
}

func TestExpectOutsideTestFailsTheScript(t *testing.T) {
	e, _ := newTestEngine()
	results, err := e.run(src(`test("ok", () => {}); expect(1).toBe(2)`), nil, 0)
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindException || se.Msg != "AssertionError: expected 1 to be 2" {
		t.Fatalf("got %v", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("results recorded before the failure must be kept: %+v", results)
	}
}

func TestTimeoutInsideTestAbortsTheScript(t *testing.T) {
	e, _ := newTestEngine()
	req := &request.Request{Method: "GET", URL: "http://a.example/", Headers: http.Header{}}
	_, err := e.RunPre(src(`test("spin", () => { while (true) {} })`), req, Context{Vars: mapVars{}, Timeout: 50 * time.Millisecond})
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindTimeout {
		t.Fatalf("a timeout must abort the script, not fail one test: %v", err)
	}
}

func TestResultsResetBetweenRuns(t *testing.T) {
	e, _ := newTestEngine()
	first, _ := e.run(src(`test("a", () => {})`), nil, 0)
	second, _ := e.run(src(`1`), nil, 0)
	if len(first) != 1 || len(second) != 0 {
		t.Fatalf("first=%v second=%v", first, second)
	}
}

func TestTestArgumentErrors(t *testing.T) {
	e, _ := newTestEngine()
	_, err := e.run(src(`test("x")`), nil, 0)
	if err == nil || !strings.Contains(err.Error(), `test("x"): second argument must be a function`) {
		t.Fatalf("got %v", err)
	}
}
