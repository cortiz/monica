package runner_test

import (
	"reflect"
	"testing"

	"jmpeax.com/sec/monica/pkg/runner"
)

func TestBailAndFilter(t *testing.T) {
	srv, seen := apiServer(t)
	dead := closedURL(t)
	mon := "# @name list-users\nGET " + srv.URL + "/users\n" +
		"###\n# @name broken\nGET " + dead + "/x\n" +
		"### Get one user\nGET " + srv.URL + "/users/1\n" +
		"###\nDELETE " + srv.URL + "/users/1\n"
	second := "# @name later\nGET " + srv.URL + "/later\n"

	tests := []struct {
		name      string
		opts      runner.Opts
		wantSent  []string
		wantNames []string // reported, in order
	}{
		{
			name:      "no controls",
			wantSent:  []string{"GET /users", "GET /users/1", "DELETE /users/1", "GET /later"},
			wantNames: []string{"list-users", "broken", "Get one user", "DELETE " + srv.URL + "/users/1", "later"},
		},
		{
			name:      "bail stops at the first failure, across files",
			opts:      runner.Opts{Bail: true},
			wantSent:  []string{"GET /users"},
			wantNames: []string{"list-users", "broken"},
		},
		{
			name:      "filter by @name glob",
			opts:      runner.Opts{Filters: []string{"list-*"}},
			wantSent:  []string{"GET /users"},
			wantNames: []string{"list-users"},
		},
		{
			name:      "filter by title or METHOD URL, several globs",
			opts:      runner.Opts{Filters: []string{"Get one *", "DELETE *"}},
			wantSent:  []string{"GET /users/1", "DELETE /users/1"},
			wantNames: []string{"Get one user", "DELETE " + srv.URL + "/users/1"},
		},
		{
			name:      "? matches one character, * crosses /",
			opts:      runner.Opts{Filters: []string{"GET *://*/users/?"}},
			wantSent:  []string{"GET /users/1"},
			wantNames: []string{"Get one user"},
		},
		{
			name:      "filter matching nothing",
			opts:      runner.Opts{Filters: []string{"nope"}},
			wantSent:  nil,
			wantNames: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(*seen)
			rec := &reportRecorder{}
			opts := tt.opts
			r := runner.NewWithReporter(&opts, rec)
			_ = r.RunFile(writeMon(t, mon))
			_ = r.RunFile(writeMon(t, second))
			if err := r.Finish(); err != nil {
				t.Fatal(err)
			}
			if got := (*seen)[before:]; !equal(got, tt.wantSent) {
				t.Errorf("sent %v, want %v", got, tt.wantSent)
			}
			var names []string
			for _, q := range rec.got {
				names = append(names, q.Name)
			}
			if !reflect.DeepEqual(names, tt.wantNames) {
				t.Errorf("reported %v, want %v", names, tt.wantNames)
			}
			if r.Bailed() != (tt.opts.Bail) {
				t.Errorf("Bailed() = %v", r.Bailed())
			}
			if rec.summary == nil || rec.summary.Requests != len(tt.wantNames) {
				t.Errorf("summary = %+v", rec.summary)
			}
		})
	}
}
